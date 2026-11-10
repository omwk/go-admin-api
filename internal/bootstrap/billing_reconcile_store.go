package bootstrap

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const reconcileRunColumns = "id,tenant_id,service_date,posting_date,cutoff_at,actor_id,trigger,status,last_call_id,calls,differences,finished_at,command_key"
const reconcileDiffColumns = "id,run_id,tenant_id,app_id,currency,service_date,posting_date,call_id,kind,expected,actual,reference_id,fingerprint,status,created_at,resolved_at,resolved_by"

func (s *billingReconcileStore) BeginRun(ctx context.Context, in billing.ReconcileRun) (billing.ReconcileRun, error) {
	err := s.database.Transaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		if in.CommandKey != "" {
			old, found, err := billingCommandRun(q, in)
			if err != nil {
				return err
			}
			if found {
				in = old
				return nil
			}
		}
		if in.Trigger == "daily" {
			key := "billing:daily:" + strconv.FormatInt(in.TenantID, 10) + ":" + in.ServiceDate.Format("2006-01-02")
			if err := q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", key).Error; err != nil {
				return billing.Unavailable()
			}
			var old billing.ReconcileRun
			err := q.Table("billing_reconcile_run").Select(reconcileRunColumns).Where("tenant_id=? AND service_date=? AND trigger='daily'", in.TenantID, in.ServiceDate).Order("id DESC").Take(&old).Error
			if err == nil {
				in = old
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return billing.Unavailable()
			}
		}
		return billingDBError(q.Table("billing_reconcile_run").Create(&in).Error)
	})
	return in, err
}

func billingCommandRun(q *gorm.DB, in billing.ReconcileRun) (billing.ReconcileRun, bool, error) {
	key := "billing:run:" + strconv.FormatInt(in.TenantID, 10) + ":" + strconv.FormatInt(in.ActorID, 10) + ":" + in.CommandKey
	if err := q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", key).Error; err != nil {
		return in, false, billing.Unavailable()
	}
	var old billing.ReconcileRun
	err := q.Table("billing_reconcile_run").Select(reconcileRunColumns).Where("tenant_id=? AND actor_id=? AND command_key=?", in.TenantID, in.ActorID, in.CommandKey).Take(&old).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return in, false, nil
	}
	if err != nil {
		return in, false, billing.Unavailable()
	}
	if !old.ServiceDate.Equal(in.ServiceDate) {
		return in, false, billing.Mismatch()
	}
	return old, true, nil
}

func (s *billingReconcileStore) Observe(ctx context.Context, run billing.ReconcileRun, rows []billing.Difference, cursor string, count int64) error {
	return s.database.Transaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		for i := range rows {
			d := &rows[i]
			d.RunID, d.TenantID, d.ServiceDate, d.PostingDate, d.CreatedAt, d.Status = run.ID, run.TenantID, run.ServiceDate, run.PostingDate, run.CutoffAt, "open"
			d.Fingerprint = billingObservationHash(*d)
		}
		if len(rows) > 0 {
			if err := q.Table("billing_reconcile_diff").Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&rows, 500).Error; err != nil {
				return billing.Unavailable()
			}
			if err := q.Exec("INSERT INTO billing_reconcile_alert(diff_id) SELECT id FROM billing_reconcile_diff WHERE run_id=? ON CONFLICT DO NOTHING", run.ID).Error; err != nil {
				return billing.Unavailable()
			}
		}
		if count == 0 {
			return nil
		}
		return billingDBError(q.Table("billing_reconcile_run").Where("id=? AND tenant_id=? AND status='running' AND last_call_id<?", run.ID, run.TenantID, cursor).Updates(map[string]any{"last_call_id": cursor, "calls": gorm.Expr("calls+?", count)}).Error)
	})
}

func (s *billingReconcileStore) CompleteRun(ctx context.Context, run billing.ReconcileRun, at time.Time) (billing.ReconcileRun, error) {
	err := s.database.Transaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		var current billing.ReconcileRun
		if err := q.Table("billing_reconcile_run").Select(reconcileRunColumns).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", run.TenantID, run.ID).Take(&current).Error; err != nil {
			return billingDBError(err)
		}
		if current.Status == "completed" {
			run = current
			return nil
		}
		if err := s.aggregateDay(tx, run); err != nil {
			return err
		}
		var diffs int64
		if err := q.Table("billing_reconcile_diff").Where("run_id=?", run.ID).Count(&diffs).Error; err != nil {
			return billing.Unavailable()
		}
		if err := q.Table("billing_reconcile_run").Where("id=?", run.ID).Updates(map[string]any{"status": "completed", "finished_at": at, "differences": diffs}).Error; err != nil {
			return billing.Unavailable()
		}
		run = current
		run.Status, run.FinishedAt, run.Differences = "completed", &at, diffs
		return nil
	})
	return run, err
}

func (s *billingReconcileStore) PendingAlerts(ctx context.Context, tenant, after int64) ([]billing.Difference, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []billing.Difference{}
	err := q.Table("billing_reconcile_diff").Select(reconcileDiffColumns).Where("tenant_id=? AND id>? AND id IN (SELECT diff_id FROM billing_reconcile_alert WHERE delivered_at IS NULL)", tenant, after).Order("id").Limit(200).Find(&rows).Error
	return rows, billingDBError(err)
}

func (s *billingReconcileStore) AcknowledgeAlert(ctx context.Context, id int64) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Table("billing_reconcile_alert").Where("diff_id=?", id).Update("delivered_at", time.Now().UTC())
	if result.Error != nil || result.RowsAffected != 1 {
		return billing.Unavailable()
	}
	return nil
}

func (s *billingReconcileStore) Differences(ctx context.Context, a billing.Actor, date string) ([]billing.Difference, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []billing.Difference{}
	query := q.Table("billing_reconcile_diff").Select(reconcileDiffColumns).Where("tenant_id=? AND app_id IN (SELECT app_id FROM api_client WHERE tenant_id=? AND owner_user_id=?)", a.TenantID, a.TenantID, a.UserID)
	if date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return nil, billing.Invalid()
		}
		query = query.Where("service_date=?", date)
	}
	err := query.Order("id DESC").Limit(100).Find(&rows).Error
	return rows, billingDBError(err)
}

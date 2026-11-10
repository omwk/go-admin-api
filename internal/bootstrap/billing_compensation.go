package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"gorm.io/gorm"
)

const compensationColumns = "id,tenant_id,diff_id,actor_id,occurred_at,action,reason,before_value,after_value,idempotency_key,verification_run_id"

func (s *billingReconcileStore) Compensate(ctx context.Context, a billing.Actor, id int64, reason, key string, at time.Time) (billing.Compensation, error) {
	var out billing.Compensation
	err := s.database.Transaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		if err := q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "compensation:"+key).Error; err != nil {
			return billing.Unavailable()
		}
		err := q.Table("billing_compensation").Select(compensationColumns).Where("tenant_id=? AND idempotency_key=?", a.TenantID, key).Take(&out).Error
		if err == nil {
			if out.DiffID != id || out.Reason != reason || out.ActorID != a.UserID {
				return billing.Mismatch()
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return billing.Unavailable()
		}
		d, err := s.ownedDifference(tx, a, id)
		if err != nil {
			return err
		}
		if d.Status != "open" || d.Kind != "charge_count" || d.Expected != 1 || d.Actual != 0 {
			return billing.TerminalConflict()
		}
		c, existed, err := s.restoreCharge(tx, a, d)
		if err != nil {
			return err
		}
		run, err := s.BeginRun(tx, billing.ReconcileRun{TenantID: a.TenantID, ActorID: a.UserID, ServiceDate: d.ServiceDate, PostingDate: billingUTCDay(at), CutoffAt: at, Trigger: "compensation", Status: "running"})
		if err != nil {
			return err
		}
		fact, err := s.singleFact(tx, run, d.CallID)
		if err != nil {
			return err
		}
		if len(billing.CheckCall(fact, at.Unix())) != 0 {
			return billing.TerminalConflict()
		}
		beforeCount, action := 0, "restore_charge"
		if existed {
			beforeCount, action = 1, "verify_charge"
		}
		before, err := json.Marshal(map[string]any{"charge_count": beforeCount, "call_id": d.CallID})
		if err != nil {
			return billing.Unavailable()
		}
		after, err := json.Marshal(c)
		if err != nil {
			return billing.Unavailable()
		}
		out = billing.Compensation{TenantID: a.TenantID, DiffID: id, ActorID: a.UserID, OccurredAt: at, Action: action, Reason: reason, BeforeValue: before, AfterValue: after, IdempotencyKey: key, VerificationRunID: run.ID}
		if err := q.Table("billing_compensation").Create(&out).Error; err != nil {
			return billing.Unavailable()
		}
		result := q.Table("billing_reconcile_diff").Where("id=? AND tenant_id=? AND status='open'", id, a.TenantID).Updates(map[string]any{"status": "resolved", "resolved_at": at, "resolved_by": a.UserID})
		if result.Error != nil || result.RowsAffected != 1 {
			return billing.TerminalConflict()
		}
		if err := q.Table("billing_reconcile_run").Where("id=?", run.ID).Updates(map[string]any{"status": "completed", "calls": 1, "last_call_id": d.CallID, "finished_at": at}).Error; err != nil {
			return billing.Unavailable()
		}
		return audit.Record(tx, s.database, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: "billing.reconcile.compensate", ResourceType: "billing_reconcile_diff", ResourceID: key, Before: map[string]any{"diff_id": id, "charge_count": beforeCount}, After: map[string]any{"charge_count": 1, "compensation_id": out.ID, "verification_run_id": run.ID}, Result: "success"})
	})
	return out, err
}

func (s *billingReconcileStore) restoreCharge(ctx context.Context, a billing.Actor, d billing.Difference) (billing.Charge, bool, error) {
	old, err := (&billingStore{database: s.database}).Charge(ctx, a.TenantID, d.CallID)
	if err == nil {
		return old, true, nil
	}
	if errs.From(err).Code != errs.From(billing.Missing()).Code {
		return old, false, err
	}
	call, err := (&gatewayStore{database: s.database}).Call(ctx, d.CallID)
	if err != nil {
		return billing.Charge{}, false, err
	}
	if call.TenantID != a.TenantID || call.AppID != d.AppID || call.BillingMode != "per_call" || !call.Admitted || call.State == "processing" {
		return billing.Charge{}, false, billing.TerminalConflict()
	}
	c := gatewayCharge(call)
	c.PreCents, c.ActualCents, c.Source, c.EntitlementID, c.ReservedUnits = call.PriceCents*call.ChargeUnits, call.CostCents, call.ChargeSource, call.EntitlementID, call.CostUnits
	c.State, c.FinishedAt = "settled", call.FinishedAt
	if call.State == "failed" {
		c.State = "released"
		if call.ErrorCode == "80302" {
			c.State = "expired"
		}
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if c.Source == "wallet" {
		w, err := (&billingStore{database: s.database}).Wallet(ctx, a.TenantID, d.AppID)
		if err != nil {
			return c, false, err
		}
		c.WalletID = w.ID
	} else {
		var reserve billing.UnitTxn
		if err := q.Table("unit_txn").Select(unitTxnColumns).Where("tenant_id=? AND call_id=? AND type='reserve'", a.TenantID, d.CallID).Take(&reserve).Error; err != nil {
			return c, false, billingDBError(err)
		}
		c.ReservedUnits = reserve.Amount
	}
	if err := q.Table("call_charge").Create(&c).Error; err != nil {
		return c, false, billing.TerminalConflict()
	}
	return c, false, nil
}

func (s *billingReconcileStore) ownedDifference(ctx context.Context, a billing.Actor, id int64) (billing.Difference, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var out billing.Difference
	err := q.Table("billing_reconcile_diff").Select(reconcileDiffColumns).Where("tenant_id=? AND id=? AND app_id IN (SELECT app_id FROM api_client WHERE tenant_id=? AND owner_user_id=?)", a.TenantID, id, a.TenantID, a.UserID).Take(&out).Error
	return out, billingDBError(err)
}

func (s *billingReconcileStore) Compensations(ctx context.Context, a billing.Actor, id int64) ([]billing.Compensation, error) {
	if _, err := s.ownedDifference(ctx, a, id); err != nil {
		return nil, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []billing.Compensation{}
	err := q.Table("billing_compensation").Select(compensationColumns).Where("tenant_id=? AND diff_id=?", a.TenantID, id).Order("id DESC").Limit(100).Find(&rows).Error
	return rows, billingDBError(err)
}

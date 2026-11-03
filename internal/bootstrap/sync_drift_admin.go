package bootstrap

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *syncEngineStore) ListDrifts(ctx context.Context, p repo.PageQuery) (repo.Result, error) {
	q, cancel, err := s.humanQuery(ctx, "schema_drift_event", "SyncAdmin.ListDrifts")
	if err != nil {
		return repo.Result{}, err
	}
	defer cancel()
	q = applySyncFilter(q, p)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return repo.Result{}, err
	}
	rows := []repo.DriftEvent{}
	err = q.Select("x.id,x.shop_id,x.baseline_id,x.kind,x.path,x.old_type,x.new_type,x.severity,x.status,x.sample_raw_id,x.detected_at").Order("x.detected_at DESC,x.id DESC").Limit(p.Size).Offset((p.Number - 1) * p.Size).Find(&rows).Error
	return repo.Result{Items: rows, Total: total, Page: p.Number, PageSize: p.Size}, err
}

func (s *syncEngineStore) ChangeDrift(ctx context.Context, id int64, status string, op *idem.Operation) error {
	if status != "acked" && status != "resolved" && status != "ignored" {
		return errs.Invalid(errors.New("invalid drift status"))
	}
	return s.schemaMutation(ctx, op, func(ctx context.Context) error {
		q, cancel, err := s.humanQuery(ctx, "schema_drift_event", "SyncAdmin.ChangeDrift")
		if err != nil {
			return err
		}
		defer cancel()
		var row repo.DriftEvent
		if err := q.Select("x.id,x.shop_id,x.status").Where("x.id=?", id).Take(&row).Error; err != nil {
			return schemaReadError(err)
		}
		human, err := identityscope.From(ctx)
		if err != nil {
			return err
		}
		write, stop := s.database.WithContext(ctx)
		defer stop()
		if _, err := lockSchema(write, human.Tenant, row.ShopID); err != nil {
			return err
		}
		values := map[string]any{"status": status}
		if status != "acked" {
			values["resolved_at"] = time.Now().UTC()
			values["resolved_by"] = human.User
		}
		if err := syncChanged(write.Table("schema_drift_event").Where("id=? AND tenant_id=? AND status=?", id, human.Tenant, row.Status).Updates(values)); err != nil {
			return err
		}
		action := map[string]string{"acked": "ack", "resolved": "resolve", "ignored": "ignore"}[status]
		return s.schemaAudit(ctx, "sync:drift:"+action, "schema_drift_event", id, map[string]any{"status": status})
	})
}

func (s *syncEngineStore) PromoteBaseline(ctx context.Context, id int64, op *idem.Operation) error {
	return s.schemaMutation(ctx, op, func(ctx context.Context) error {
		q, cancel, err := s.humanQuery(ctx, "raw_dedup", "SyncAdmin.PromoteBaseline")
		if err != nil {
			return err
		}
		defer cancel()
		var candidate schemaBaseline
		err = q.Joins("JOIN schema_baseline b ON b.sample_raw_id=x.raw_id AND b.tenant_id=x.tenant_id AND b.platform_id=shop.platform_id AND b.resource_type=x.resource_type").Select("b.id,b.tenant_id,b.platform_id,b.resource_type,b.version,b.fingerprint,b.fields,b.sample_raw_id,b.status").Where("b.id=?", id).Take(&candidate).Error
		if err != nil {
			return schemaReadError(err)
		}
		write, stop := s.database.WithContext(ctx)
		defer stop()
		var platform int64
		if err := write.Table("platform").Select("id").Where("id=?", candidate.PlatformID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&platform).Error; err != nil {
			return err
		}
		if err := write.Table("schema_baseline").Select(schemaBaselineColumns).Where("id=? AND tenant_id=?", id, candidate.TenantID).Take(&candidate).Error; err != nil {
			return err
		}
		if candidate.Status == "active" {
			return nil
		}
		where := "tenant_id=? AND platform_id=? AND resource_type=? AND status='active'"
		if err := write.Table("schema_baseline").Where(where, candidate.TenantID, platform, candidate.ResourceType).Update("status", "superseded").Error; err != nil {
			return err
		}
		if err := syncChanged(write.Table("schema_baseline").Where("id=? AND tenant_id=? AND status='superseded'", id, candidate.TenantID).Update("status", "active")); err != nil {
			return err
		}
		return s.schemaAudit(ctx, "sync:baseline:promote", "schema_baseline", id, map[string]any{"fingerprint": candidate.Fingerprint, "version": candidate.Version})
	})
}

func schemaReadError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return syncMissing()
	}
	return err
}

func (s *syncEngineStore) schemaMutation(ctx context.Context, op *idem.Operation, fn func(context.Context) error) error {
	human, err := identityscope.From(ctx)
	if err != nil {
		return err
	}
	if op != nil && (op.UserID != human.User || op.TenantID != human.Tenant) {
		return syncDenied()
	}
	err = s.database.Transaction(ctx, func(ctx context.Context) error {
		if op != nil {
			claim, err := newIdempotencyStore(s.database).Claim(ctx, *op)
			if err != nil {
				return err
			}
			if claim.Action == idem.Replay {
				return nil
			}
		}
		if err := fn(ctx); err != nil {
			return err
		}
		if op == nil {
			return nil
		}
		result, err := idem.JSONResult(200, map[string]bool{"ok": true})
		if err != nil {
			return err
		}
		_, err = newIdempotencyStore(s.database).Complete(ctx, *op, result)
		return err
	})
	if errors.Is(err, idem.ErrMismatch) {
		return errs.New(errs.ErrIdempotencyMismatch, 409, "幂等请求不一致")
	}
	if errors.Is(err, idem.ErrProcessing) {
		return errs.New(errs.ErrConflict, 409, "请求处理中")
	}
	return err
}

func (s *syncEngineStore) schemaAudit(ctx context.Context, action, resource string, id int64, after map[string]any) error {
	human, err := identityscope.From(ctx)
	if err != nil {
		return err
	}
	return audit.Record(ctx, s.database, audit.Event{TenantID: human.Tenant, ActorType: "user", ActorID: &human.User, Action: action, ResourceType: resource, ResourceID: strconv.FormatInt(id, 10), After: after, Result: "success"})
}

var _ repo.DriftStore = (*syncEngineStore)(nil)

package bootstrap

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const planColumns = "id,tenant_id,code,name,currency,price_cents,quota_type,unit_count,valid_days,route_id,status,version"

func (s *billingStore) Plans(ctx context.Context, a billing.Actor) ([]billing.Plan, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []billing.Plan{}
	err := q.Table("pricing_plan").Select(planColumns).Where("tenant_id=?", a.TenantID).Order("id DESC").Limit(100).Find(&rows).Error
	return rows, billingDBError(err)
}

func (s *billingStore) SavePlan(ctx context.Context, a billing.Actor, p billing.Plan) (billing.Plan, error) {
	err := s.Transaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		if err := billingPlanRoute(q, a.TenantID, p.RouteID); err != nil {
			return err
		}
		p.TenantID = a.TenantID
		if p.ID == 0 {
			p.Version = 1
			result := q.Table("pricing_plan").Clauses(clause.OnConflict{DoNothing: true}).Create(&p)
			if result.Error != nil {
				return billing.Unavailable()
			}
			if result.RowsAffected != 1 {
				return billing.TerminalConflict()
			}
		} else {
			result := q.Table("pricing_plan").Where("tenant_id=? AND id=? AND version=?", a.TenantID, p.ID, p.Version).Updates(map[string]any{"code": p.Code, "name": p.Name, "currency": p.Currency, "price_cents": p.PriceCents, "quota_type": p.QuotaType, "unit_count": p.UnitCount, "valid_days": p.ValidDays, "route_id": p.RouteID, "status": p.Status, "version": p.Version + 1})
			if result.Error != nil {
				return billing.Unavailable()
			}
			if result.RowsAffected != 1 {
				return errs.New(10409, 409, "资源版本冲突")
			}
			p.Version++
		}
		return audit.Record(tx, s.database, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: "billing.plan.save", ResourceType: "pricing_plan", ResourceID: strconv.FormatInt(p.ID, 10), After: map[string]any{"version": p.Version, "price_cents": p.PriceCents, "unit_count": p.UnitCount}, Result: "success"})
	})
	return p, err
}

func billingPlanRoute(q *gorm.DB, tenant, id int64) error {
	if id == 0 {
		return nil
	}
	var count int64
	if err := q.Table("api_route").Where("tenant_id=? AND id=?", tenant, id).Count(&count).Error; err != nil {
		return billing.Unavailable()
	}
	if count != 1 {
		return billing.Missing()
	}
	return nil
}

func (s *billingStore) Grant(ctx context.Context, a billing.Actor, in billing.GrantInput, key, hash string, at time.Time) (billing.Entitlement, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if err := q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "grant:"+key).Error; err != nil {
		return billing.Entitlement{}, billing.Unavailable()
	}
	var old billing.Entitlement
	err := q.Table("entitlement").Select(entitlementColumns).Where("grant_key=? AND tenant_id=?", key, a.TenantID).Take(&old).Error
	if err == nil {
		if old.RequestHash != hash {
			return old, billing.Mismatch()
		}
		return old, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return old, billing.Unavailable()
	}
	if in.EndsAt != nil && (!in.EndsAt.After(in.StartsAt) || !in.EndsAt.After(at)) {
		return old, billing.Invalid()
	}
	w, err := s.OpenWallet(ctx, a, in.AppID, at)
	if err != nil {
		return old, err
	}
	if w.Status != "active" {
		return old, billing.Inactive()
	}
	e, err := newBillingEntitlement(q, a, in, key, hash, at)
	if err != nil {
		return e, err
	}
	if err := q.Table("entitlement").Create(&e).Error; err != nil {
		return e, billing.Unavailable()
	}
	if e.Source == "purchase" {
		_, err = s.Apply(ctx, billing.Change{Txn: billing.Txn{TenantID: a.TenantID, WalletID: w.ID, Type: "adjust", AmountCents: -e.PurchaseCents, BalanceDeltaCents: -e.PurchaseCents, IdempotencyKey: "billing:purchase:" + key, RequestHash: hash, ActorID: a.UserID, Note: "entitlement purchase", OccurredAt: at, BizType: "subscription", BizID: strconv.FormatInt(e.ID, 10)}, Currency: e.Currency, RequireActive: true})
		if err != nil {
			return e, err
		}
	}
	if err := appendUnit(q, e, "grant", e.TotalUnits, e.TotalUnits, 0, "", at, at); err != nil {
		return e, err
	}
	err = audit.Record(ctx, s.database, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: "billing.entitlement.grant", ResourceType: "entitlement", ResourceID: strconv.FormatInt(e.ID, 10), After: map[string]any{"kind": e.Kind, "total_units": e.TotalUnits, "plan_id": e.PlanID, "plan_version": e.PlanVersion, "purchase_cents": e.PurchaseCents}, Result: "success"})
	return e, err
}

func newBillingEntitlement(q *gorm.DB, a billing.Actor, in billing.GrantInput, key, hash string, at time.Time) (billing.Entitlement, error) {
	e := billing.Entitlement{TenantID: a.TenantID, AppID: in.AppID, Currency: billing.Currency, Kind: in.Kind, QuotaType: in.QuotaType, TotalUnits: in.Units, StartsAt: in.StartsAt, EndsAt: in.EndsAt, RouteID: in.RouteID, Source: "gift", SourceRef: in.SourceRef, Status: "active", Version: 1, CreatedBy: a.UserID, CreatedAt: at, GrantKey: key, RequestHash: hash}
	if in.PlanID > 0 {
		var p billing.Plan
		if err := q.Table("pricing_plan").Select(planColumns).Clauses(clause.Locking{Strength: "SHARE"}).Where("tenant_id=? AND id=? AND status='active'", a.TenantID, in.PlanID).Take(&p).Error; err != nil {
			return e, billingDBError(err)
		}
		e.Kind, e.QuotaType, e.TotalUnits, e.RouteID = "paid", p.QuotaType, p.UnitCount, p.RouteID
		e.Source, e.SourceRef, e.PlanID, e.PlanVersion, e.PurchaseCents = "purchase", p.Code, p.ID, p.Version, p.PriceCents
		e.StartsAt, e.EndsAt = at, nil
		if p.ValidDays > 0 {
			end := at.AddDate(0, 0, p.ValidDays)
			e.EndsAt = &end
		}
	}
	if err := billingPlanRoute(q, a.TenantID, e.RouteID); err != nil {
		return e, err
	}
	e.RemainingUnits = e.TotalUnits
	return e, nil
}

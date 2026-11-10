package bootstrap

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const entitlementColumns = "id,tenant_id,app_id,currency,kind,quota_type,total_units,remaining_units,reserved_units,starts_at,ends_at,route_id,source,source_ref,plan_id,plan_version,purchase_cents,status,version,created_by,created_at,grant_key,request_hash"
const unitTxnColumns = "id,tenant_id,app_id,currency,entitlement_id,call_id,type,amount,remaining_delta,reserved_delta,remaining_after,reserved_after,version_after,idempotency_key,occurred_at,service_date"

func (s *billingStore) ReserveSource(ctx context.Context, c *billing.Charge, at time.Time) error {
	if !db.InTransaction(ctx) {
		return billing.Unavailable()
	}
	if c.Source != "pending" {
		return nil
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var e billing.Entitlement
	err := q.Table("entitlement").Select(entitlementColumns).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id=? AND app_id=? AND currency=? AND status='active' AND starts_at<=? AND (ends_at IS NULL OR ends_at>?) AND route_id IN (0,?) AND remaining_units-reserved_units>=CASE WHEN quota_type='calls' THEN 1 ELSE ? END", c.TenantID, c.AppID, c.Currency, at, at, c.RouteID, c.Units).
		Order("CASE kind WHEN 'free' THEN 0 ELSE 1 END,ends_at ASC NULLS LAST,id").Take(&e).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return billing.Unavailable()
	}
	if err == nil {
		amount := c.Units
		if e.QuotaType == "calls" {
			amount = 1
		}
		if err := changeUnits(q, &e, "reserve", amount, c.CallID, at, c.CreatedAt); err != nil {
			return err
		}
		c.Source, c.EntitlementID, c.ReservedUnits = e.Kind, e.ID, amount
	} else {
		w, err := s.Wallet(ctx, c.TenantID, c.AppID)
		if err != nil {
			return err
		}
		c.Source, c.WalletID = "wallet", w.ID
	}
	result := q.Table("call_charge").Where("tenant_id=? AND call_id=? AND source='pending' AND state='reserved'", c.TenantID, c.CallID).
		Updates(map[string]any{"source": c.Source, "wallet_id": c.WalletID, "entitlement_id": c.EntitlementID, "reserved_units": c.ReservedUnits})
	if result.Error != nil || result.RowsAffected != 1 {
		return billing.Unavailable()
	}
	return nil
}

func (s *billingStore) FinishUnits(ctx context.Context, c billing.Charge, state string, at time.Time) error {
	if !db.InTransaction(ctx) {
		return billing.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var e billing.Entitlement
	if err := q.Table("entitlement").Select(entitlementColumns).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND app_id=? AND id=? AND currency=?", c.TenantID, c.AppID, c.EntitlementID, c.Currency).Take(&e).Error; err != nil {
		return billingDBError(err)
	}
	kind := "release"
	if state == "settled" {
		kind = "consume"
	}
	if err := changeUnits(q, &e, kind, c.ReservedUnits, c.CallID, at, c.CreatedAt); err != nil {
		return err
	}
	return expireEntitlement(q, &e, at)
}

func (s *billingStore) ExpireUnits(ctx context.Context, at time.Time) (int, error) {
	if !db.InTransaction(ctx) {
		return 0, billing.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []billing.Entitlement{}
	if err := q.Table("entitlement").Select(entitlementColumns).Clauses(clause.Locking{Strength: "UPDATE"}).Where("status='active' AND ends_at<=?", at).Order("ends_at,id").Limit(500).Find(&rows).Error; err != nil {
		return 0, billing.Unavailable()
	}
	for i := range rows {
		if err := expireEntitlement(q, &rows[i], at); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

func expireEntitlement(q *gorm.DB, e *billing.Entitlement, at time.Time) error {
	if e.EndsAt == nil || e.EndsAt.After(at) {
		return nil
	}
	if available := e.RemainingUnits - e.ReservedUnits; available > 0 {
		if err := changeUnits(q, e, "expire", available, "", at, *e.EndsAt); err != nil {
			return err
		}
	}
	result := q.Table("entitlement").Where("tenant_id=? AND id=?", e.TenantID, e.ID).Update("status", "expired")
	if result.Error != nil || result.RowsAffected != 1 {
		return billing.Unavailable()
	}
	e.Status = "expired"
	return nil
}

func changeUnits(q *gorm.DB, e *billing.Entitlement, kind string, amount int64, call string, at, service time.Time) error {
	remaining, reserved := int64(0), int64(0)
	switch kind {
	case "grant", "refund":
		remaining = amount
	case "reserve":
		reserved = amount
	case "release":
		reserved = -amount
	case "consume":
		remaining, reserved = -amount, -amount
	case "expire":
		remaining = -amount
	default:
		return billing.Invalid()
	}
	if amount <= 0 {
		return billing.Invalid()
	}
	result := q.Table("entitlement").Where("tenant_id=? AND id=? AND version=? AND remaining_units::numeric+?>=reserved_units::numeric+? AND reserved_units::numeric+?>=0 AND remaining_units::numeric+? BETWEEN 0 AND total_units", e.TenantID, e.ID, e.Version, remaining, reserved, reserved, remaining).
		Updates(map[string]any{"remaining_units": gorm.Expr("remaining_units+?", remaining), "reserved_units": gorm.Expr("reserved_units+?", reserved), "version": gorm.Expr("version+1")})
	if result.Error != nil || result.RowsAffected != 1 {
		return billing.FrozenInsufficient()
	}
	e.RemainingUnits += remaining
	e.ReservedUnits += reserved
	e.Version++
	if e.RemainingUnits == 0 && e.Status == "active" {
		if err := q.Table("entitlement").Where("id=? AND tenant_id=?", e.ID, e.TenantID).Update("status", "exhausted").Error; err != nil {
			return billing.Unavailable()
		}
		e.Status = "exhausted"
	}
	return appendUnit(q, *e, kind, amount, remaining, reserved, call, at, service)
}

func appendUnit(q *gorm.DB, e billing.Entitlement, kind string, amount, remaining, reserved int64, call string, at, service time.Time) error {
	u := billing.UnitTxn{TenantID: e.TenantID, AppID: e.AppID, Currency: e.Currency, EntitlementID: e.ID, CallID: call, Type: kind, Amount: amount, RemainingDelta: remaining, ReservedDelta: reserved, RemainingAfter: e.RemainingUnits, ReservedAfter: e.ReservedUnits, VersionAfter: e.Version,
		IdempotencyKey: "billing:unit:" + strconv.FormatInt(e.ID, 10) + ":" + strconv.Itoa(e.Version), OccurredAt: at, ServiceDate: billingUTCDay(service)}
	return billingDBError(q.Table("unit_txn").Create(&u).Error)
}

func billingUTCDay(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}

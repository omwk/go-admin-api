package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"gorm.io/gorm/clause"
)

const billingChargeColumns = "call_id,tenant_id,wallet_id,app_id,currency,route_id,route_version,price_cents,units,pre_cents,state,actual_cents,created_at,deadline_at,finished_at"

func (s *billingStore) Prepare(ctx context.Context, c *billing.Charge) error {
	if !db.InTransaction(ctx) {
		return billing.Unavailable()
	}
	w, err := s.Wallet(ctx, c.TenantID, c.AppID)
	if err != nil {
		return err
	}
	c.WalletID = w.ID
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Table("call_charge").Clauses(clause.OnConflict{DoNothing: true}).Create(c)
	if result.Error != nil {
		return billing.Unavailable()
	}
	if result.RowsAffected == 1 {
		return nil
	}
	old, err := s.Charge(ctx, c.TenantID, c.CallID)
	if err != nil {
		return err
	}
	if !billing.SameCharge(old, *c) || old.WalletID != c.WalletID || !old.CreatedAt.Equal(c.CreatedAt) || !old.DeadlineAt.Equal(c.DeadlineAt) {
		return billing.Mismatch()
	}
	return nil
}

func (s *billingStore) Charge(ctx context.Context, tenant int64, id string) (billing.Charge, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if db.InTransaction(ctx) {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var out billing.Charge
	err := q.Table("call_charge").Select(billingChargeColumns).Where("tenant_id=? AND call_id=?", tenant, id).Take(&out).Error
	return out, billingDBError(err)
}

func (s *billingStore) EndCharge(ctx context.Context, c billing.Charge) error {
	if !db.InTransaction(ctx) {
		return billing.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Table("call_charge").Where("tenant_id=? AND call_id=? AND state='reserved'", c.TenantID, c.CallID).Updates(map[string]any{"state": c.State, "actual_cents": c.ActualCents, "finished_at": c.FinishedAt})
	if result.Error != nil || result.RowsAffected != 1 {
		return billing.Unavailable()
	}
	return nil
}

package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
)

type gatewayBilling struct {
	money    *billing.Service
	database *db.DB
}

func (s *gatewayBilling) Balance(ctx context.Context, p gateway.Principal) (gateway.OpenBalance, error) {
	b, err := s.money.Balance(ctx, p.TenantID, p.AppID)
	return gateway.OpenBalance{Currency: b.Currency, BalanceCents: b.BalanceCents, AvailableCents: b.AvailableCents, FrozenCents: b.FrozenCents}, err
}

func (s *gatewayBilling) Redeem(ctx context.Context, p gateway.Principal, code string) (gateway.OpenBalance, error) {
	b, err := s.money.Redeem(ctx, p.TenantID, p.AppID, code)
	return gateway.OpenBalance{Currency: b.Currency, BalanceCents: b.BalanceCents, AvailableCents: b.AvailableCents, FrozenCents: b.FrozenCents}, err
}

func newGatewayBilling(database *db.DB, now func() time.Time) (*gatewayBilling, error) {
	money, err := billing.NewService(&billingStore{database: database}, now)
	return &gatewayBilling{money: money, database: database}, err
}

func newGatewayReconciler(database *db.DB, now func() time.Time) (*gateway.CallReconciler, error) {
	charges, err := newGatewayBilling(database, now)
	if err != nil {
		return nil, err
	}
	return gateway.NewCallReconciler(&gatewayStore{database: database}, now, charges)
}

func gatewayCharge(c gateway.Call) billing.Charge {
	return billing.Charge{CallID: c.ID, TenantID: c.TenantID, AppID: c.AppID, Currency: c.Currency, RouteID: c.RouteID, RouteVersion: c.RouteVersion, PriceCents: c.PriceCents, Units: c.ChargeUnits, CreatedAt: c.CalledAt, DeadlineAt: c.DeadlineAt}
}

func (s *gatewayBilling) Prepare(ctx context.Context, c gateway.Call) error {
	return s.money.Prepare(ctx, gatewayCharge(c))
}
func (s *gatewayBilling) Freeze(ctx context.Context, c gateway.Call) error {
	return s.money.Freeze(ctx, gatewayCharge(c))
}
func (s *gatewayBilling) Finish(ctx context.Context, c gateway.Call) error {
	if c.State == "succeeded" {
		return s.money.Settle(ctx, c.TenantID, c.ID, c.PriceCents*c.ChargeUnits)
	}
	return s.money.Release(ctx, c.TenantID, c.ID, c.ErrorCode == "80302")
}

func (s *gatewayBilling) Confirm(ctx context.Context, c gateway.Call) error {
	state := "reserved"
	if c.State == "succeeded" {
		state = "settled"
	} else if c.State == "failed" {
		state = "released"
		if c.ErrorCode == "80302" {
			state = "expired"
		}
	}
	if err := s.money.Confirm(ctx, gatewayCharge(c), state); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var txns int64
	expected := int64(1)
	if state != "reserved" {
		expected = 2
	}
	if err := q.Table("wallet_txn").Where("tenant_id=? AND call_id=?", c.TenantID, c.ID).Count(&txns).Error; err != nil || txns != expected {
		return gateway.Unavailable()
	}
	if state == "settled" {
		var meters int64
		if err := q.Table("api_call_meter").Where("tenant_id=? AND call_id=?", c.TenantID, c.ID).Count(&meters).Error; err != nil || meters != 1 {
			return gateway.Unavailable()
		}
	}
	return nil
}

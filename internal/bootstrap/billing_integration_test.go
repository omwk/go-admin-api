package bootstrap

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/stretchr/testify/require"
)

type billingFixture struct {
	*gatewayFixture
	money  *billing.Service
	ledger *billingStore
	wallet billing.Wallet
}

func newBillingFixture(t *testing.T, amount int64) *billingFixture {
	t.Helper()
	f := &billingFixture{gatewayFixture: newGatewayFixture(t)}
	f.ledger = &billingStore{database: f.database}
	var err error
	f.money, err = billing.NewService(f.ledger, func() time.Time { return f.now })
	require.NoError(t, err)
	f.wallet, err = f.money.Open(f.ctx, billing.Actor{TenantID: 1, UserID: f.user.ID}, f.client.AppID)
	require.NoError(t, err)
	if amount > 0 {
		f.wallet, err = f.money.Topup(f.ctx, billing.Actor{TenantID: 1, UserID: f.user.ID}, f.wallet.ID, amount, "initial funding", "initial")
		require.NoError(t, err)
	}
	return f
}

func (f *billingFixture) charge() billing.Charge {
	return billing.Charge{CallID: ulid.Make().String(), TenantID: 1, AppID: f.client.AppID, Currency: billing.Currency, RouteID: 1, RouteVersion: 1, PriceCents: 5, Units: 1, CreatedAt: f.now, DeadlineAt: f.now.Add(time.Minute)}
}

func (f *billingFixture) assertFunds(t *testing.T, balance, frozen int64) {
	t.Helper()
	w, err := f.ledger.Wallet(f.ctx, 1, f.client.AppID)
	require.NoError(t, err)
	require.Equal(t, balance, w.BalanceCents)
	require.Equal(t, frozen, w.FrozenCents)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var sum struct{ Balance, Frozen int64 }
	require.NoError(t, q.Table("wallet_txn").Select("coalesce(sum(balance_delta_cents),0) AS balance,coalesce(sum(frozen_delta_cents),0) AS frozen").Where("wallet_id=?", w.ID).Scan(&sum).Error)
	require.Equal(t, w.BalanceCents, sum.Balance)
	require.Equal(t, w.FrozenCents, sum.Frozen)
	var inconsistent int64
	require.NoError(t, q.Raw(`SELECT count(*) FROM (SELECT balance_after_cents,frozen_after_cents,
		sum(balance_delta_cents) OVER (ORDER BY version_after) b,sum(frozen_delta_cents) OVER (ORDER BY version_after) f
		FROM wallet_txn WHERE wallet_id=?) x WHERE balance_after_cents<>b OR frozen_after_cents<>f`, w.ID).Scan(&inconsistent).Error)
	require.Zero(t, inconsistent)
}

func TestBillingIntegrationDifferenceAndIdempotentTerminal(t *testing.T) {
	// REQ-BILL-001/002/003: H=5/C=3 releases the difference, never another call's reserve.
	f := newBillingFixture(t, 100)
	c := f.charge()
	require.NoError(t, f.money.Freeze(f.ctx, c))
	require.NoError(t, f.money.Freeze(f.ctx, c))
	f.assertFunds(t, 100, 5)
	require.NoError(t, f.money.Settle(f.ctx, 1, c.CallID, 3))
	require.NoError(t, f.money.Settle(f.ctx, 1, c.CallID, 3))
	require.Error(t, f.money.Release(f.ctx, 1, c.CallID, false))
	f.assertFunds(t, 97, 0)
	c2 := f.charge()
	require.NoError(t, f.money.Freeze(f.ctx, c2))
	require.NoError(t, f.money.Release(f.ctx, 1, c2.CallID, false))
	require.NoError(t, f.money.Release(f.ctx, 1, c2.CallID, false))
	f.assertFunds(t, 97, 0)
}

func TestBillingIntegrationConditionalConcurrentFunds(t *testing.T) {
	// REQ-BILL-002: two service instances share only PG, with 30 simultaneous calls.
	f := newBillingFixture(t, 100)
	second, err := billing.NewService(&billingStore{database: f.database}, func() time.Time { return f.now })
	require.NoError(t, err)
	start := make(chan struct{})
	out := make(chan error, 30)
	var group sync.WaitGroup
	for i := 0; i < 30; i++ {
		c := f.charge()
		svc := []*billing.Service{f.money, second}[i%2]
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			err := svc.Freeze(f.ctx, c)
			if err == nil {
				err = svc.Settle(f.ctx, 1, c.CallID, 5)
			}
			out <- err
		}()
	}
	close(start)
	group.Wait()
	close(out)
	var succeeded, insufficient int
	for err := range out {
		if err == nil {
			succeeded++
		} else {
			require.Equal(t, 90102, errs.From(err).Code)
			insufficient++
		}
	}
	require.Equal(t, 20, succeeded)
	require.Equal(t, 10, insufficient)
	f.assertFunds(t, 0, 0)
}

func TestBillingIntegrationRollbackOverflowAndOwnership(t *testing.T) {
	f := newBillingFixture(t, 100)
	c := f.charge()
	require.Error(t, f.database.Transaction(f.ctx, func(tx context.Context) error {
		if err := f.money.Freeze(tx, c); err != nil {
			return err
		}
		return billing.Unavailable()
	}))
	f.assertFunds(t, 100, 0)
	_, err := f.money.Topup(f.ctx, billing.Actor{TenantID: 1, UserID: f.user.ID}, f.wallet.ID, math.MaxInt64, "overflow", "overflow")
	require.Error(t, err)
	f.assertFunds(t, 100, 0)
	_, err = f.money.Topup(f.ctx, billing.Actor{TenantID: 2, UserID: f.user.ID}, f.wallet.ID, 1, "other tenant", "other")
	require.Error(t, err)
	_, err = f.money.Topup(f.ctx, billing.Actor{TenantID: 1, UserID: f.user.ID + 100}, f.wallet.ID, 1, "other owner", "other")
	require.Error(t, err)
	c.PriceCents = 101
	require.Equal(t, 90102, errs.From(f.money.Freeze(f.ctx, c)).Code)
	f.assertFunds(t, 100, 0)
}

func TestBillingIntegrationRechargeAndCodeAtomicity(t *testing.T) {
	// REQ-BILL-007: duplicate topups and credentials never mint funds twice.
	f := newBillingFixture(t, 100)
	a := billing.Actor{TenantID: 1, UserID: f.user.ID}
	for i := 0; i < 2; i++ {
		_, err := f.money.Topup(f.ctx, a, f.wallet.ID, 50, "funding", "one-key")
		require.NoError(t, err)
	}
	_, err := f.money.Topup(f.ctx, a, f.wallet.ID, 51, "funding", "one-key")
	require.Equal(t, 10006, errs.From(err).Code)
	codes, err := f.money.Mint(f.ctx, a, 25, 1, nil)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = f.money.Redeem(f.ctx, 1, f.client.AppID, codes[0].Code)
		if i == 0 {
			require.NoError(t, err)
		} else {
			require.Equal(t, 90301, errs.From(err).Code)
		}
	}
	f.assertFunds(t, 175, 0)
	codes, err = f.money.Mint(f.ctx, a, 25, 1, nil)
	require.NoError(t, err)
	require.Error(t, f.database.Transaction(f.ctx, func(tx context.Context) error {
		if _, err := f.money.Redeem(tx, 1, f.client.AppID, codes[0].Code); err != nil {
			return err
		}
		return billing.Unavailable()
	}))
	f.assertFunds(t, 175, 0)
	_, err = f.money.Redeem(f.ctx, 1, f.client.AppID, codes[0].Code)
	require.NoError(t, err)
	f.assertFunds(t, 200, 0)
}

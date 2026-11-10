package bootstrap

import (
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/stretchr/testify/require"
)

func billingConcurrent(count int, run func() error) []error {
	start := make(chan struct{})
	results := make([]error, count)
	var group sync.WaitGroup
	for i := range results {
		group.Add(1)
		go func() { defer group.Done(); <-start; results[i] = run() }()
	}
	close(start)
	group.Wait()
	return results
}

func TestBillingIntegrationConcurrentSameCall(t *testing.T) {
	// REQ-BILL-003: 100 competing retries retain one freeze and one settle.
	f := newBillingFixture(t, 100)
	c := f.charge()
	for _, err := range billingConcurrent(100, func() error { return f.money.Freeze(f.ctx, c) }) {
		require.NoError(t, err)
	}
	f.assertFunds(t, 100, 5)
	for _, err := range billingConcurrent(100, func() error { return f.money.Settle(f.ctx, 1, c.CallID, 5) }) {
		require.NoError(t, err)
	}
	f.assertFunds(t, 95, 0)
	require.Error(t, f.money.Settle(f.ctx, 1, c.CallID, 4))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("wallet_txn").Where("call_id=?", c.CallID).Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestBillingIntegrationCodeExpiryTenantAndConcurrentUse(t *testing.T) {
	// REQ-BILL-007: expiry, tenant and one-use checks precede any cash credit.
	f := newBillingFixture(t, 100)
	a := billing.Actor{TenantID: 1, UserID: f.user.ID}
	expires := f.now.Add(time.Minute)
	codes, err := f.money.Mint(f.ctx, a, 25, 1, &expires)
	require.NoError(t, err)
	expired, err := billing.NewService(f.ledger, func() time.Time { return expires })
	require.NoError(t, err)
	_, err = expired.Redeem(f.ctx, 1, f.client.AppID, codes[0].Code)
	require.Equal(t, 90301, errs.From(err).Code)
	f.assertFunds(t, 100, 0)
	codes, err = f.money.Mint(f.ctx, a, 25, 1, nil)
	require.NoError(t, err)
	_, err = f.money.Redeem(f.ctx, 2, f.client.AppID, codes[0].Code)
	require.Equal(t, 90301, errs.From(err).Code)
	var successes int
	for _, err := range billingConcurrent(20, func() error {
		_, err := f.money.Redeem(f.ctx, 1, f.client.AppID, codes[0].Code)
		return err
	}) {
		if err == nil {
			successes++
		} else {
			require.Equal(t, 90301, errs.From(err).Code)
		}
	}
	require.Equal(t, 1, successes)
	f.assertFunds(t, 125, 0)
	other, _, err := f.service.Create(f.ctx, f.actor(), "other-billing-app", f.user.ID, nil)
	require.NoError(t, err)
	wallet, err := f.money.Open(f.ctx, a, other.AppID)
	require.NoError(t, err)
	_, err = f.money.Redeem(f.ctx, 1, other.AppID, codes[0].Code)
	require.Equal(t, 90301, errs.From(err).Code)
	untouched, err := f.ledger.OwnedWallet(f.ctx, a, wallet.ID)
	require.NoError(t, err)
	require.Zero(t, untouched.BalanceCents)
	f.assertFunds(t, 125, 0)
}

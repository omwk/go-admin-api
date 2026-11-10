package bootstrap

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func billingGift(t *testing.T, f *billingHTTPFixture, kind string, units int64, end *time.Time) billing.Entitlement {
	t.Helper()
	e, err := f.money.Grant(f.ctx, billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}, billing.GrantInput{AppID: f.client.AppID, Kind: kind, QuotaType: "calls", Units: units, EndsAt: end, SourceRef: "acceptance grant"}, "gift-"+kind+"-"+strconv.FormatInt(units, 10))
	require.NoError(t, err)
	return e
}

func billingSucceed(t *testing.T, f *billingHTTPFixture) billing.Charge {
	t.Helper()
	c := f.intent()
	require.NoError(t, f.calls.Start(f.ctx, c))
	require.NoError(t, f.calls.Finish(f.ctx, c.ID, gateway.CallResult{StatusCode: 200, Attempts: 1, Data: json.RawMessage(`{"status":"completed"}`)}))
	charge, err := f.money.Receipt(f.ctx, 1, c.ID)
	require.NoError(t, err)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Raw("SELECT (SELECT count(*) FROM wallet_txn WHERE call_id=? AND type='settle')+(SELECT count(*) FROM unit_txn WHERE call_id=? AND type='consume')", c.ID, c.ID).Scan(&count).Error)
	require.EqualValues(t, 1, count, "each call consumes exactly one source")
	return charge
}

func TestBillingEntitlementIntegrationPriorityAndSingleSource(t *testing.T) {
	// REQ-BILL-004: ten free calls, two paid calls, then explicit per-call cash fallback.
	f := newBillingHTTPFixture(t, 100)
	free := billingGift(t, f, "free", 10, nil)
	paid := billingGift(t, f, "paid", 2, nil)
	for i := 0; i < 13; i++ {
		charge := billingSucceed(t, f)
		switch {
		case i < 10:
			require.Equal(t, free.ID, charge.EntitlementID)
			require.Equal(t, "free", charge.Source)
		case i < 12:
			require.Equal(t, paid.ID, charge.EntitlementID)
			require.Equal(t, "paid", charge.Source)
		default:
			require.Equal(t, "wallet", charge.Source)
		}
	}
	f.assertFunds(t, 95, 0)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var units, cents int64
	require.NoError(t, q.Table("api_call_log").Select("sum(cost_units)").Scan(&units).Error)
	require.NoError(t, q.Table("api_call_log").Select("sum(cost_cents)").Scan(&cents).Error)
	require.EqualValues(t, 12, units)
	require.EqualValues(t, 5, cents)
}

func TestBillingEntitlementIntegrationExpiryOrderAndFragments(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	late, early := f.now.Add(2*time.Hour), f.now.Add(time.Hour)
	later := billingGift(t, f, "paid", 5, &late)
	first := billingGift(t, f, "paid", 3, &early)
	require.Equal(t, first.ID, billingSucceed(t, f).EntitlementID)
	f.now = early
	n, err := f.money.Expire(f.ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, later.ID, billingSucceed(t, f).EntitlementID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var e billing.Entitlement
	require.NoError(t, q.Table("entitlement").Select(entitlementColumns).Where("id=?", first.ID).Take(&e).Error)
	require.Equal(t, "expired", e.Status)
	require.Zero(t, e.RemainingUnits)
	// Units cannot be split even when the sum of fragments covers the request.
	f.route.UnitsPerCall = 7
	for _, n := range []int64{3, 4} {
		_, err := f.money.Grant(f.ctx, billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}, billing.GrantInput{AppID: f.client.AppID, Kind: "free", QuotaType: "units", Units: n, SourceRef: "fragment"}, "fragment-"+strconv.FormatInt(n, 10))
		require.NoError(t, err)
	}
	// The calls-based entitlement still covers a complete call independently of its route units.
	require.Equal(t, later.ID, billingSucceed(t, f).EntitlementID)
	f.now = late
	_, err = f.money.Expire(f.ctx)
	require.NoError(t, err)
	require.Equal(t, "wallet", billingSucceed(t, f).Source)
	f.assertFunds(t, 65, 0)
}

func TestBillingEntitlementIntegrationInFlightExpiryAndRelease(t *testing.T) {
	f := newBillingHTTPFixture(t, 0)
	end := f.now.Add(time.Second)
	e := billingGift(t, f, "free", 10, &end)
	c := f.intent()
	require.NoError(t, f.calls.Start(f.ctx, c))
	f.now = end
	_, err := f.money.Expire(f.ctx)
	require.NoError(t, err)
	require.NoError(t, f.calls.Finish(f.ctx, c.ID, gateway.CallResult{StatusCode: 200, Data: json.RawMessage(`{"ok":true}`)}))
	c2 := f.intent()
	require.Error(t, f.calls.Start(f.ctx, c2))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var left struct{ RemainingUnits, ReservedUnits int64 }
	require.NoError(t, q.Table("entitlement").Select("remaining_units,reserved_units").Where("id=?", e.ID).Take(&left).Error)
	require.Zero(t, left.RemainingUnits)
	require.Zero(t, left.ReservedUnits)
	f.assertFunds(t, 0, 0)
}

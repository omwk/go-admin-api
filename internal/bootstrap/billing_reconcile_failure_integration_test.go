package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/stretchr/testify/require"
)

func TestBillingReconcileIntegrationUnitsOrphansAndOffsettingCents(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	billingGift(t, f, "free", 2, nil)
	unit := billingSucceed(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	// The independent intent survives corruption of the usage record.
	require.NoError(t, q.Exec("UPDATE api_call_log SET cost_units=cost_units+1 WHERE call_id=?", unit.CallID).Error)
	billingSucceed(t, f)
	first, second := billingSucceed(t, f), billingSucceed(t, f)
	require.NoError(t, q.Exec("ALTER TABLE wallet_txn DISABLE TRIGGER wallet_txn_append_only").Error)
	require.NoError(t, q.Exec("UPDATE wallet_txn SET amount_cents=amount_cents+CASE WHEN call_id=? THEN 1 ELSE -1 END,balance_delta_cents=balance_delta_cents-CASE WHEN call_id=? THEN 1 ELSE -1 END WHERE call_id IN (?,?) AND type='settle'", first.CallID, first.CallID, first.CallID, second.CallID).Error)
	require.NoError(t, q.Exec("ALTER TABLE wallet_txn ENABLE TRIGGER wallet_txn_append_only").Error)
	// Delete both derived log and independent intent; the meter and ledger must still be found.
	require.NoError(t, q.Exec("DELETE FROM api_call_log WHERE call_id=?", second.CallID).Error)
	require.NoError(t, q.Exec("DELETE FROM api_call WHERE call_id=?", second.CallID).Error)
	alerts := []billing.Difference{}
	run := billingRun(t, f, billingReconciler(t, f, &alerts), f.now.Format("2006-01-02"))
	require.Positive(t, run.Differences)
	kinds := map[string]bool{}
	for _, d := range alerts {
		kinds[d.Kind] = true
	}
	require.True(t, kinds["log_units"])
	require.True(t, kinds["cash_cents"])
	require.True(t, kinds["call_count"])
	require.True(t, kinds["log_count"])
}

func TestBillingReconcileIntegrationPendingAlertRetryAndCommandResume(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	c := billingSucceed(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("DELETE FROM call_charge WHERE call_id=?", c.CallID).Error)
	a := billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}
	store := &billingReconcileStore{database: f.database}
	failed, err := billing.NewReconciler(store, func() time.Time { return f.now }, func(context.Context, billing.Difference) error { return errors.New("alert temporarily unavailable") })
	require.NoError(t, err)
	first, err := failed.Manual(f.ctx, a, f.now.Format("2006-01-02"), "retry")
	require.Error(t, err)
	require.Equal(t, "completed", first.Status)
	alerts := []billing.Difference{}
	svc := billingReconciler(t, f, &alerts)
	second, err := svc.Manual(f.ctx, a, f.now.Format("2006-01-02"), "retry")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Len(t, alerts, 1)
	other := billing.Actor{TenantID: 1, UserID: a.UserID + 100}
	_, err = svc.Compensate(f.ctx, other, alerts[0].ID, "unauthorized evidence access", "foreign")
	require.Error(t, err)
	rows, err := svc.Differences(f.ctx, other, "")
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestBillingReconcileIntegrationDailyScheduleAndExpiry(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	end := f.now.Add(time.Minute)
	e := billingGift(t, f, "free", 2, &end)
	billingSucceed(t, f)
	date := f.now.Format("2006-01-02")
	next := billingUTCDay(f.now).Add(26 * time.Hour)
	for i := 0; i < 2; i++ {
		require.NoError(t, runBillingSchedule(f.ctx, f.database, next))
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var runs int64
	require.NoError(t, q.Table("billing_reconcile_run").Where("tenant_id=1 AND service_date=? AND trigger='daily'", date).Count(&runs).Error)
	require.EqualValues(t, 1, runs)
	var status string
	require.NoError(t, q.Table("entitlement").Select("status").Where("id=?", e.ID).Scan(&status).Error)
	require.Equal(t, "expired", status)
	var diffs int64
	require.NoError(t, q.Table("billing_reconcile_diff").Count(&diffs).Error)
	require.Zero(t, diffs)
}

func TestBillingEntitlementMigrationIntegrationEvidenceAndDownUp(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	c := billingSucceed(t, f)
	billingGift(t, f, "free", 2, nil)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, statement := range []string{"UPDATE unit_txn SET amount=1", "DELETE FROM unit_txn", "TRUNCATE unit_txn", "UPDATE entitlement SET remaining_units=-1", "UPDATE entitlement SET reserved_units=3"} {
		require.Error(t, q.Exec(statement).Error)
	}
	state, err := f.database.Migrate(f.ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 19, state.Version)
	var absent bool
	require.NoError(t, q.Raw("SELECT to_regclass('entitlement') IS NULL AND to_regclass('unit_txn') IS NULL AND to_regclass('billing_reconcile_diff') IS NULL").Scan(&absent).Error)
	require.True(t, absent)
	state, err = f.database.Migrate(f.ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, 20, state.Version)
	require.False(t, state.Dirty)
	var snapshot struct {
		ChargeSource string
		CostCents    int64
	}
	require.NoError(t, q.Table("api_call").Select("charge_source,cost_cents").Where("call_id=?", c.CallID).Take(&snapshot).Error)
	require.Equal(t, "wallet", snapshot.ChargeSource, "M9a cash calls retain their source across the upgrade")
	require.EqualValues(t, 5, snapshot.CostCents)
	alerts := []billing.Difference{}
	run := billingRun(t, f, billingReconciler(t, f, &alerts), f.now.Format("2006-01-02"))
	require.Zero(t, run.Differences)
	var cash int64
	require.NoError(t, q.Table("usage_daily").Select("charged_cents").Where("app_id=?", f.client.AppID).Scan(&cash).Error)
	require.EqualValues(t, 5, cash)
}

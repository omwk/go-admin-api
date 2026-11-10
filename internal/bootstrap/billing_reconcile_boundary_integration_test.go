package bootstrap

import (
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func TestBillingReconcileIntegrationInFlightSourceSnapshot(t *testing.T) {
	for _, source := range []string{"wallet", "free"} {
		t.Run(source, func(t *testing.T) {
			f := newBillingHTTPFixture(t, 100)
			if source == "free" {
				billingGift(t, f, "free", 2, nil)
			}
			c := f.intent()
			require.NoError(t, f.calls.Start(f.ctx, c))
			alerts := []billing.Difference{}
			svc := billingReconciler(t, f, &alerts)
			run := billingRun(t, f, svc, f.now.Format("2006-01-02"))
			require.Zero(t, run.Differences, "a valid in-flight reservation is consistent before its deadline")
			require.Empty(t, alerts)
			f.now = c.DeadlineAt.Add(time.Second)
			run = billingRun(t, f, svc, c.CalledAt.Format("2006-01-02"))
			require.EqualValues(t, 1, run.Differences)
			require.Equal(t, "overdue_call", alerts[0].Kind)
		})
	}
}

func TestBillingReconcileIntegrationSourceAttribution(t *testing.T) {
	for _, statement := range []string{
		"UPDATE call_charge SET source='pending' WHERE call_id=?",
		"UPDATE api_call SET charge_source='none' WHERE call_id=?",
		"UPDATE api_call_log SET charge_source='none' WHERE call_id=?",
	} {
		t.Run(statement, func(t *testing.T) {
			f := newBillingHTTPFixture(t, 100)
			c := billingSucceed(t, f)
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			require.NoError(t, q.Exec(statement, c.CallID).Error)
			alerts := []billing.Difference{}
			run := billingRun(t, f, billingReconciler(t, f, &alerts), f.now.Format("2006-01-02"))
			require.Positive(t, run.Differences)
			found := false
			for _, d := range alerts {
				found = found || d.CallID == c.CallID && d.Kind == "attribution"
			}
			require.True(t, found, "terminal source snapshots must agree across all sides")
		})
	}
}

func TestBillingReconcileIntegrationOneUnitLedgerDifference(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	f.route.UnitsPerCall = 2
	_, err := f.money.Grant(f.ctx, billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}, billing.GrantInput{AppID: f.client.AppID, Kind: "free", QuotaType: "units", Units: 10, SourceRef: "unit reconciliation"}, "unit-ledger")
	require.NoError(t, err)
	c := billingSucceed(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	// Only this disposable database's owner can bypass append-only protection to simulate corruption.
	require.NoError(t, q.Exec("ALTER TABLE unit_txn DISABLE TRIGGER unit_txn_append_only").Error)
	require.NoError(t, q.Exec("UPDATE unit_txn SET amount=amount-1,remaining_delta=remaining_delta+1,reserved_delta=reserved_delta+1 WHERE call_id=? AND type='consume'", c.CallID).Error)
	require.NoError(t, q.Exec("ALTER TABLE unit_txn ENABLE TRIGGER unit_txn_append_only").Error)
	alerts := []billing.Difference{}
	run := billingRun(t, f, billingReconciler(t, f, &alerts), f.now.Format("2006-01-02"))
	require.Positive(t, run.Differences)
	found := false
	for _, d := range alerts {
		if d.CallID == c.CallID && d.Kind == "consumed_units" {
			require.EqualValues(t, 2, d.Expected)
			require.EqualValues(t, 1, d.Actual)
			require.Equal(t, "open", d.Status)
			found = true
		}
	}
	require.True(t, found)
}

func TestBillingEntitlementIntegrationExpiredFailureReleasesOnce(t *testing.T) {
	f := newBillingHTTPFixture(t, 0)
	end := f.now.Add(time.Second)
	e := billingGift(t, f, "free", 10, &end)
	c := f.intent()
	require.NoError(t, f.calls.Start(f.ctx, c))
	f.now = end
	_, err := f.money.Expire(f.ctx)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		require.NoError(t, f.calls.Finish(f.ctx, c.ID, gateway.CallResult{StatusCode: 503, ErrorCode: "10001"}))
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var balance struct{ RemainingUnits, ReservedUnits int64 }
	require.NoError(t, q.Table("entitlement").Select("remaining_units,reserved_units").Where("id=?", e.ID).Take(&balance).Error)
	require.Zero(t, balance.RemainingUnits)
	require.Zero(t, balance.ReservedUnits)
	var releases int64
	require.NoError(t, q.Table("unit_txn").Where("call_id=? AND type='release'", c.ID).Count(&releases).Error)
	require.EqualValues(t, 1, releases)
	alerts := []billing.Difference{}
	run := billingRun(t, f, billingReconciler(t, f, &alerts), f.now.Format("2006-01-02"))
	require.Zero(t, run.Differences)
	f.assertFunds(t, 0, 0)
}

func TestBillingReconcileIntegrationRepeatedObservationCredentials(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	c := billingSucceed(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("DELETE FROM call_charge WHERE call_id=?", c.CallID).Error)
	alerts := []billing.Difference{}
	svc := billingReconciler(t, f, &alerts)
	for i := 0; i < 2; i++ {
		billingRun(t, f, svc, f.now.Format("2006-01-02"))
	}
	require.Len(t, alerts, 2)
	a := billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}
	first, err := svc.Compensate(f.ctx, a, alerts[0].ID, "restore original receipt", "first")
	require.NoError(t, err)
	require.Equal(t, "restore_charge", first.Action)
	second, err := svc.Compensate(f.ctx, a, alerts[1].ID, "verify restored receipt against all sides", "second")
	require.NoError(t, err)
	require.Equal(t, "verify_charge", second.Action)
	var open int64
	require.NoError(t, q.Table("billing_reconcile_diff").Where("status='open'").Count(&open).Error)
	require.Zero(t, open)
	var settlements int64
	require.NoError(t, q.Table("wallet_txn").Where("call_id=? AND type='settle'", c.CallID).Count(&settlements).Error)
	require.EqualValues(t, 1, settlements)
	f.assertFunds(t, 95, 0)
}

func TestBillingReconcileIntegrationMonthlyWatermark(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	day := billingUTCDay(f.now)
	start := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	// Completed observations on either side of a gap cannot close a monthly watermark.
	require.NoError(t, q.Exec("INSERT INTO billing_reconcile_run(tenant_id,service_date,posting_date,cutoff_at,actor_id,trigger,status) SELECT 1,d::date,?::date,?,0,'daily','completed' FROM generate_series(?::timestamp,?::timestamp,interval '1 day') d WHERE d::date<>?::date", day, f.now, start, end.AddDate(0, 0, -1), start.AddDate(0, 0, 1)).Error)
	var watermark struct {
		CoveredThrough  time.Time
		CoveredDays     int64
		OpenDifferences int64
	}
	require.NoError(t, q.Table("billing_reconcile_month").Select("covered_through,covered_days").Where("tenant_id=1 AND period_start=?", start).Take(&watermark).Error)
	require.True(t, watermark.CoveredThrough.Equal(start.AddDate(0, 0, 1)))
	require.EqualValues(t, int64(end.Sub(start)/(24*time.Hour))-1, watermark.CoveredDays)
	require.NoError(t, q.Exec("INSERT INTO billing_reconcile_run(tenant_id,service_date,posting_date,cutoff_at,actor_id,trigger,status) VALUES(1,?,?,?,0,'manual','completed')", start.AddDate(0, 0, 1), day, f.now).Error)
	require.NoError(t, q.Table("billing_reconcile_month").Select("covered_through,covered_days").Where("tenant_id=1 AND period_start=?", start).Take(&watermark).Error)
	require.True(t, watermark.CoveredThrough.Equal(end))
	c := billingSucceed(t, f)
	require.NoError(t, q.Exec("DELETE FROM call_charge WHERE call_id=?", c.CallID).Error)
	alerts := []billing.Difference{}
	svc := billingReconciler(t, f, &alerts)
	billingRun(t, f, svc, f.now.Format("2006-01-02"))
	require.NoError(t, q.Table("billing_reconcile_month").Select("open_differences").Where("tenant_id=1 AND period_start=?", start).Take(&watermark).Error)
	require.EqualValues(t, 1, watermark.OpenDifferences, "full coverage does not erase unresolved observations")
	_, err := svc.Compensate(f.ctx, billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}, alerts[0].ID, "restore receipt from original evidence", "monthly")
	require.NoError(t, err)
	require.NoError(t, q.Table("billing_reconcile_month").Select("open_differences").Where("tenant_id=1 AND period_start=?", start).Take(&watermark).Error)
	require.Zero(t, watermark.OpenDifferences)
}

func TestBillingReconcileIntegrationUnprovenCompensationRollsBack(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	c := billingSucceed(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("ALTER TABLE wallet_txn DISABLE TRIGGER wallet_txn_append_only").Error)
	require.NoError(t, q.Exec("UPDATE wallet_txn SET amount_cents=amount_cents-1,balance_delta_cents=balance_delta_cents+1 WHERE call_id=? AND type='settle'", c.CallID).Error)
	require.NoError(t, q.Exec("ALTER TABLE wallet_txn ENABLE TRIGGER wallet_txn_append_only").Error)
	require.NoError(t, q.Exec("DELETE FROM call_charge WHERE call_id=?", c.CallID).Error)
	alerts := []billing.Difference{}
	svc := billingReconciler(t, f, &alerts)
	billingRun(t, f, svc, f.now.Format("2006-01-02"))
	var diff billing.Difference
	for _, d := range alerts {
		if d.CallID == c.CallID && d.Kind == "charge_count" {
			diff = d
		}
	}
	require.Positive(t, diff.ID)
	_, err := svc.Compensate(f.ctx, billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}, diff.ID, "inconsistent original evidence", "unproven")
	require.Error(t, err)
	var charges, credentials int64
	require.NoError(t, q.Table("call_charge").Where("call_id=?", c.CallID).Count(&charges).Error)
	require.NoError(t, q.Table("billing_compensation").Where("diff_id=?", diff.ID).Count(&credentials).Error)
	require.Zero(t, charges)
	require.Zero(t, credentials)
	var state string
	require.NoError(t, q.Table("billing_reconcile_diff").Select("status").Where("id=?", diff.ID).Scan(&state).Error)
	require.Equal(t, "open", state)
}

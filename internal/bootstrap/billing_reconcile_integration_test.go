package bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func billingReconciler(t *testing.T, f *billingHTTPFixture, alerts *[]billing.Difference) *billing.Reconciler {
	t.Helper()
	svc, err := billing.NewReconciler(&billingReconcileStore{database: f.database}, func() time.Time { return f.now }, func(_ context.Context, d billing.Difference) error { *alerts = append(*alerts, d); return nil })
	require.NoError(t, err)
	return svc
}

func billingRun(t *testing.T, f *billingHTTPFixture, s *billing.Reconciler, date string) billing.ReconcileRun {
	t.Helper()
	// Direct SQL assertion preserves safe database diagnostics when authoring this fixture.
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var facts []billing.CallFact
	day, err := time.Parse("2006-01-02", date)
	require.NoError(t, err)
	require.NoError(t, q.Raw(billingFactsSQL, sql.Named("tenant", 1), sql.Named("day", day), sql.Named("end", day.AddDate(0, 0, 1)), sql.Named("cutoff", f.now), sql.Named("after", "")).Scan(&facts).Error)
	run, err := s.Run(f.ctx, billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}, date, "manual")
	require.NoError(t, err)
	return run
}

func TestBillingReconcileIntegrationCleanAndOneCentAlert(t *testing.T) {
	// REQ-BILL-005/006: independent observations find one cent, without netting it away.
	f := newBillingHTTPFixture(t, 100)
	c := billingSucceed(t, f)
	alerts := []billing.Difference{}
	svc := billingReconciler(t, f, &alerts)
	date := f.now.Format("2006-01-02")
	run := billingRun(t, f, svc, date)
	require.Zero(t, run.Differences)
	require.Empty(t, alerts)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	// Owner-only corruption in this disposable test database; production ledger remains immutable.
	require.NoError(t, q.Exec("ALTER TABLE wallet_txn DISABLE TRIGGER wallet_txn_append_only").Error)
	require.NoError(t, q.Exec("UPDATE wallet_txn SET amount_cents=amount_cents-1,balance_delta_cents=balance_delta_cents+1 WHERE call_id=? AND type='settle'", c.CallID).Error)
	require.NoError(t, q.Exec("ALTER TABLE wallet_txn ENABLE TRIGGER wallet_txn_append_only").Error)
	run = billingRun(t, f, svc, date)
	require.Positive(t, run.Differences)
	found := false
	for _, d := range alerts {
		if d.CallID == c.CallID && d.Kind == "cash_cents" {
			require.EqualValues(t, 5, d.Expected)
			require.EqualValues(t, 4, d.Actual)
			require.Equal(t, "open", d.Status)
			found = true
		}
	}
	require.True(t, found)
	require.NoError(t, svc.DeliverAlerts(f.ctx, 1))
	require.Len(t, alerts, int(run.Differences), "acknowledged alerts are not redelivered")
}

func TestBillingReconcileIntegrationMissingChargeAndCompensation(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	c := billingSucceed(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("DELETE FROM call_charge WHERE call_id=?", c.CallID).Error)
	alerts := []billing.Difference{}
	svc := billingReconciler(t, f, &alerts)
	run := billingRun(t, f, svc, f.now.Format("2006-01-02"))
	require.EqualValues(t, 1, run.Differences)
	require.Len(t, alerts, 1)
	d := alerts[0]
	require.Equal(t, "charge_count", d.Kind)
	a := billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}
	credential, err := svc.Compensate(f.ctx, a, d.ID, "restore from durable call and matching original ledger", "repair")
	require.NoError(t, err)
	require.Positive(t, credential.ID)
	require.Equal(t, a.UserID, credential.ActorID)
	require.Equal(t, "restore_charge", credential.Action)
	require.Equal(t, d.ID, credential.DiffID)
	replayed, err := svc.Compensate(f.ctx, a, d.ID, credential.Reason, "repair")
	require.NoError(t, err)
	require.Equal(t, credential.ID, replayed.ID)
	rows, err := svc.Compensations(f.ctx, a, d.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	var state string
	require.NoError(t, q.Table("billing_reconcile_diff").Select("status").Where("id=?", d.ID).Scan(&state).Error)
	require.Equal(t, "resolved", state)
	require.Error(t, q.Exec("UPDATE billing_reconcile_diff SET actual=1 WHERE id=?", d.ID).Error)
	require.Error(t, q.Exec("DELETE FROM billing_compensation WHERE id=?", credential.ID).Error)
	run = billingRun(t, f, svc, f.now.Format("2006-01-02"))
	require.Zero(t, run.Differences)
	f.assertFunds(t, 95, 0)
}

func TestBillingReconcileIntegrationDualUTCDates(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	day := time.Date(f.now.Year(), f.now.Month(), f.now.Day(), 0, 0, 0, 0, time.UTC)
	f.now = day.Add(24*time.Hour - time.Second)
	c := f.intent()
	require.NoError(t, f.calls.Start(f.ctx, c))
	f.now = f.now.Add(2 * time.Second)
	require.NoError(t, f.calls.Finish(f.ctx, c.ID, gateway.CallResult{StatusCode: 200, Data: json.RawMessage(`{"ok":true}`)}))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var dates struct{ Service, Posting string }
	require.NoError(t, q.Raw("SELECT service_date::text service,posting_date::text posting FROM wallet_txn WHERE call_id=? AND type='settle'", c.ID).Scan(&dates).Error)
	require.Equal(t, day.Format("2006-01-02"), dates.Service)
	require.Equal(t, day.AddDate(0, 0, 1).Format("2006-01-02"), dates.Posting)
	alerts := []billing.Difference{}
	run := billingRun(t, f, billingReconciler(t, f, &alerts), day.Format("2006-01-02"))
	require.Zero(t, run.Differences)
	var daily struct{ Calls, MeteredCalls, MeteredCents, ChargedCents int64 }
	require.NoError(t, q.Table("usage_daily").Select("calls,metered_calls,metered_cents,charged_cents").Where("app_id=? AND service_date=?", f.client.AppID, day).Take(&daily).Error)
	require.EqualValues(t, 1, daily.Calls)
	require.EqualValues(t, 1, daily.MeteredCalls)
	require.EqualValues(t, 5, daily.MeteredCents)
	require.EqualValues(t, 5, daily.ChargedCents)
	t.Log("service_date=" + dates.Service + " posting_date=" + dates.Posting + " run=" + strconv.FormatInt(run.ID, 10))
}

package bootstrap

import (
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/stretchr/testify/require"
)

func TestBillingStatementIntegrationRetentionThreeYearsAndOpenProtection(t *testing.T) {
	f := newBillingHTTPFixture(t, 5)
	f.now = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	c := billingSucceed(t, f)
	original := billingSettledTxn(t, f, c.CallID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	count := func(at time.Time) int64 {
		var n int64
		require.NoError(t, q.Raw("SELECT count(*) FROM billing_retention_candidates(?) WHERE record_type='wallet_txn' AND record_id=?", at, original.ID).Scan(&n).Error)
		return n
	}
	require.Zero(t, count(original.OccurredAt.AddDate(3, 0, 0).Add(-time.Microsecond)))
	require.EqualValues(t, 1, count(original.OccurredAt.AddDate(3, 0, 0)))
	require.NoError(t, q.Exec("UPDATE api_call_log SET cost_cents=cost_cents+1 WHERE call_id=?", c.CallID).Error)
	alerts := []billing.Difference{}
	r := billingReconciler(t, f, &alerts)
	billingRun(t, f, r, f.now.Format("2006-01-02"))
	require.NotEmpty(t, alerts)
	require.Zero(t, count(original.OccurredAt.AddDate(4, 0, 0)), "open differences protect their source application beyond three years")
	in := billing.AdjustmentInput{OriginalTxnID: original.ID, Kind: "supplement", AmountCents: 3, DiffID: alerts[0].ID, Reason: "pending correction", Evidence: "retention-case"}
	out, err := billingStatements(t, f).Adjust(f.ctx, billingStatementActor(f), in, "retention-open")
	require.NoError(t, err)
	require.Equal(t, "open", out.Status)
	for _, query := range []string{"DELETE FROM wallet_txn WHERE id=?", "UPDATE wallet_txn SET note='changed' WHERE id=?"} {
		require.Error(t, q.Exec(query, original.ID).Error)
	}
	require.Error(t, q.Exec("DELETE FROM billing_adjustment WHERE id=?", out.ID).Error)
	require.Error(t, q.Exec("TRUNCATE billing_adjustment").Error)
	require.Zero(t, count(original.OccurredAt.AddDate(10, 0, 0)))
}

func TestBillingStatementIntegrationIssuedRetentionAndDraftProtection(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	f.now = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	billingSucceed(t, f)
	svc, a := billingStatements(t, f), billingStatementActor(f)
	st, err := svc.Generate(f.ctx, a, f.client.AppID, "2026-11")
	require.NoError(t, err)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	count := func(at time.Time) int64 {
		var n int64
		require.NoError(t, q.Raw("SELECT count(*) FROM billing_retention_candidates(?) WHERE record_type='statement' AND record_id=?", at, st.ID).Scan(&n).Error)
		return n
	}
	require.Zero(t, count(f.now.AddDate(10, 0, 0)), "an unfinished draft is protected indefinitely")
	f.now = time.Date(2026, 12, 1, 2, 0, 0, 0, time.UTC)
	billingCoverMonth(t, f, st.PeriodStart)
	st, err = svc.Issue(f.ctx, a, st.ID, st.Version, "verified month")
	require.NoError(t, err)
	require.Zero(t, count(f.now.AddDate(3, 0, 0).Add(-time.Microsecond)))
	require.EqualValues(t, 1, count(f.now.AddDate(3, 0, 0)))
	require.Error(t, q.Exec("DELETE FROM billing_statement WHERE id=?", st.ID).Error, "candidate inspection never authorizes evidence deletion")
	require.Error(t, q.Exec("DELETE FROM billing_statement_line WHERE statement_id=?", st.ID).Error)
}

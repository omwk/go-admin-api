package bootstrap

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/stretchr/testify/require"
)

func billingSettledTxn(t *testing.T, f *billingHTTPFixture, call string) billing.Txn {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var out billing.Txn
	require.NoError(t, q.Table("wallet_txn").Select(billingTxnColumns).Where("call_id=? AND type='settle'", call).Take(&out).Error)
	return out
}

func TestBillingStatementIntegrationRefundBoundAndNextPeriod(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	f.now = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	c := billingSucceed(t, f)
	original := billingSettledTxn(t, f, c.CallID)
	svc := billingStatements(t, f)
	a := billingStatementActor(f)
	st, err := svc.Generate(f.ctx, a, f.client.AppID, "2026-11")
	require.NoError(t, err)
	f.now = time.Date(2026, 12, 1, 2, 0, 0, 0, time.UTC)
	billingCoverMonth(t, f, st.PeriodStart)
	st, err = svc.Issue(f.ctx, a, st.ID, st.Version, "verified month")
	require.NoError(t, err)
	in := billing.AdjustmentInput{OriginalTxnID: original.ID, Kind: "refund", AmountCents: 2, Reason: "partial reversal", Evidence: "finance-case-1"}
	out, err := svc.Adjust(f.ctx, a, in, "refund-1")
	require.NoError(t, err)
	require.Equal(t, "applied", out.Status)
	replayed, err := svc.Adjust(f.ctx, a, in, "refund-1")
	require.NoError(t, err)
	require.Equal(t, out.ID, replayed.ID)
	in.AmountCents = 4
	_, err = svc.Adjust(f.ctx, a, in, "refund-over")
	require.Error(t, err)
	in.AmountCents = 1
	_, err = svc.Adjust(f.ctx, a, in, "refund-1")
	require.Error(t, err, "different parameters cannot reuse refund identity")
	next, err := svc.Generate(f.ctx, a, f.client.AppID, "2026-12")
	require.NoError(t, err)
	require.EqualValues(t, -2, next.TotalCents)
	detail, err := svc.Detail(f.ctx, a, next.ID, 0, 20)
	require.NoError(t, err)
	require.Len(t, detail.Lines, 1)
	require.Equal(t, st.ID, detail.Lines[0].OriginalStatementID)
	old, err := svc.Detail(f.ctx, a, st.ID, 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 5, old.TotalCents)
	f.assertFunds(t, 97, 0)
}

func TestBillingStatementIntegrationConcurrentPartialRefunds(t *testing.T) {
	f := newBillingHTTPFixture(t, 5)
	c := billingSucceed(t, f)
	original := billingSettledTxn(t, f, c.CallID)
	svc := billingStatements(t, f)
	a := billingStatementActor(f)
	results := make(chan error, 10)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 10; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			_, err := svc.Adjust(f.ctx, a, billing.AdjustmentInput{OriginalTxnID: original.ID, Kind: "refund", AmountCents: 1, Reason: "partial reversal", Evidence: "finance-case"}, "parallel-"+strconv.Itoa(i))
			results <- err
		}(i)
	}
	close(start)
	workers.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	require.Equal(t, 5, success)
	f.assertFunds(t, 5, 0)
}

func TestBillingStatementIntegrationCrossMonthSupplementPreservesIssued(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	f.now = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	c := billingSucceed(t, f)
	original := billingSettledTxn(t, f, c.CallID)
	svc, a := billingStatements(t, f), billingStatementActor(f)
	st, err := svc.Generate(f.ctx, a, f.client.AppID, "2026-11")
	require.NoError(t, err)
	f.now = time.Date(2026, 12, 1, 2, 0, 0, 0, time.UTC)
	billingCoverMonth(t, f, st.PeriodStart)
	st, err = svc.Issue(f.ctx, a, st.ID, st.Version, "verified month")
	require.NoError(t, err)
	before, err := svc.Detail(f.ctx, a, st.ID, 0, 100)
	require.NoError(t, err)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("UPDATE api_call_log SET cost_cents=cost_cents+1 WHERE call_id=?", c.CallID).Error)
	alerts := []billing.Difference{}
	r := billingReconciler(t, f, &alerts)
	billingRun(t, f, r, "2026-11-15")
	require.NotEmpty(t, alerts)
	adjustment, err := svc.Adjust(f.ctx, a, billing.AdjustmentInput{OriginalTxnID: original.ID, Kind: "supplement", AmountCents: 1, DiffID: alerts[0].ID, Reason: "approved correction", Evidence: "cross-month-case"}, "cross-month-supplement")
	require.NoError(t, err)
	require.Equal(t, "applied", adjustment.Status)
	next, err := svc.Generate(f.ctx, a, f.client.AppID, "2026-12")
	require.NoError(t, err)
	require.EqualValues(t, 1, next.TotalCents)
	detail, err := svc.Detail(f.ctx, a, next.ID, 0, 100)
	require.NoError(t, err)
	require.Len(t, detail.Lines, 1)
	require.Equal(t, "supplement", detail.Lines[0].Kind)
	require.Equal(t, st.ID, detail.Lines[0].OriginalStatementID)
	after, err := svc.Detail(f.ctx, a, st.ID, 0, 100)
	require.NoError(t, err)
	require.Equal(t, before, after)
	f.assertFunds(t, 94, 0)
}

func TestBillingStatementIntegrationSupplementOpenRetryAndEvidence(t *testing.T) {
	f := newBillingHTTPFixture(t, 5)
	c := billingSucceed(t, f)
	original := billingSettledTxn(t, f, c.CallID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	// A real discrepancy is observed from the independent log projection.
	require.NoError(t, q.Exec("UPDATE api_call_log SET cost_cents=cost_cents+1 WHERE call_id=?", c.CallID).Error)
	alerts := []billing.Difference{}
	r := billingReconciler(t, f, &alerts)
	billingRun(t, f, r, f.now.Format("2006-01-02"))
	require.NotEmpty(t, alerts)
	svc := billingStatements(t, f)
	a := billingStatementActor(f)
	in := billing.AdjustmentInput{OriginalTxnID: original.ID, Kind: "supplement", AmountCents: 3, DiffID: alerts[0].ID, Reason: "approved correction", Evidence: "finance-case-2"}
	blank := in
	blank.Evidence = ""
	_, err := svc.Adjust(f.ctx, a, blank, "no-evidence")
	require.Error(t, err)
	out, err := svc.Adjust(f.ctx, a, in, "debit")
	require.NoError(t, err)
	require.Equal(t, "open", out.Status)
	require.Zero(t, out.LedgerTxnID)
	var state string
	require.NoError(t, q.Table("billing_reconcile_diff").Select("status").Where("id=?", in.DiffID).Scan(&state).Error)
	require.Equal(t, "open", state)
	_, err = f.money.Topup(f.ctx, a, f.wallet.ID, 10, "correction funding", "correction-funding")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		out, err = svc.ApplyAdjustment(f.ctx, a, out.ID)
		require.NoError(t, err)
		require.Equal(t, "applied", out.Status)
	}
	f.assertFunds(t, 7, 0)
	require.NoError(t, q.Table("billing_reconcile_diff").Select("status").Where("id=?", in.DiffID).Scan(&state).Error)
	require.Equal(t, "resolved", state)
	credentials, err := r.Compensations(f.ctx, a, in.DiffID)
	require.NoError(t, err)
	require.Len(t, credentials, 1)
	require.Contains(t, string(credentials[0].AfterValue), in.Evidence)
	require.Error(t, q.Exec("UPDATE billing_adjustment SET amount_cents=1 WHERE id=?", out.ID).Error)
}

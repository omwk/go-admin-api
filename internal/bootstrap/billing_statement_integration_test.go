package bootstrap

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func billingStatements(t *testing.T, f *billingHTTPFixture) *billing.StatementService {
	t.Helper()
	svc, err := billing.NewStatementService(&billingStatementStore{database: f.database}, func() time.Time { return f.now })
	require.NoError(t, err)
	return svc
}

func billingStatementActor(f *billingHTTPFixture) billing.Actor {
	return billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}
}

func billingCoverMonth(t *testing.T, f *billingHTTPFixture, start time.Time) {
	t.Helper()
	alerts := []billing.Difference{}
	r := billingReconciler(t, f, &alerts)
	for day := start; day.Before(start.AddDate(0, 1, 0)); day = day.AddDate(0, 0, 1) {
		_, err := r.Manual(f.ctx, billingStatementActor(f), day.Format("2006-01-02"), "statement-"+day.Format("2006-01-02"))
		require.NoError(t, err)
	}
	require.Empty(t, alerts)
}

func TestBillingStatementIntegrationDraftExactRebuildAndScope(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	f.now = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	billingSucceed(t, f)
	billingGift(t, f, "free", 2, nil)
	billingSucceed(t, f)
	svc := billingStatements(t, f)
	a := billingStatementActor(f)
	st, err := svc.Generate(f.ctx, a, f.client.AppID, "2026-11")
	require.NoError(t, err)
	require.EqualValues(t, 5, st.TotalCents)
	require.EqualValues(t, 1, st.TotalUnits)
	detail, err := svc.Detail(f.ctx, a, st.ID, 0, 1)
	require.NoError(t, err)
	require.Len(t, detail.Lines, 1)
	require.Positive(t, detail.NextCursor)
	next, err := svc.Detail(f.ctx, a, st.ID, detail.NextCursor, 100)
	require.NoError(t, err)
	require.Len(t, next.Lines, 1)
	require.Equal(t, st.TotalCents, detail.Lines[0].AmountCents+next.Lines[0].AmountCents)
	oldHash := st.SourceHash
	st, err = svc.Generate(f.ctx, a, f.client.AppID, "2026-11")
	require.NoError(t, err)
	require.Equal(t, oldHash, st.SourceHash)
	require.EqualValues(t, 5, st.TotalCents)
	foreign := billing.Actor{TenantID: 1, UserID: a.UserID + 100}
	_, err = svc.Detail(f.ctx, foreign, st.ID, 0, 20)
	require.Error(t, err)
	list, err := svc.List(f.ctx, foreign, billing.StatementFilter{})
	require.NoError(t, err)
	require.Empty(t, list.Items)
}

func TestBillingStatementIntegrationIssueImmutableAndLateCall(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	f.now = time.Date(2026, 11, 30, 23, 59, 58, 0, time.UTC)
	billingSucceed(t, f)
	svc := billingStatements(t, f)
	a := billingStatementActor(f)
	st, err := svc.Generate(f.ctx, a, f.client.AppID, "2026-11")
	require.NoError(t, err)
	_, err = svc.Issue(f.ctx, a, st.ID, st.Version, "month close")
	require.Error(t, err)
	f.now = time.Date(2026, 12, 1, 1, 0, 0, 0, time.UTC)
	billingCoverMonth(t, f, st.PeriodStart)
	f.now = time.Date(2026, 11, 30, 23, 59, 59, 0, time.UTC)
	late := f.intent()
	require.NoError(t, f.calls.Start(f.ctx, late))
	f.now = time.Date(2026, 12, 1, 1, 0, 0, 0, time.UTC)
	_, err = svc.Issue(f.ctx, a, st.ID, st.Version, "month close")
	require.Error(t, err, "unsettled intent blocks issuance")
	require.NoError(t, f.calls.Finish(f.ctx, late.ID, billingFailedResult()))
	st, err = svc.Issue(f.ctx, a, st.ID, st.Version, "month close")
	require.NoError(t, err)
	require.Equal(t, "issued", st.Status)
	_, err = svc.Generate(f.ctx, a, f.client.AppID, "2026-11")
	require.Error(t, err)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, query := range []string{"UPDATE billing_statement SET total_cents=0 WHERE id=?", "DELETE FROM billing_statement WHERE id=?", "UPDATE billing_statement_line SET amount_cents=0 WHERE statement_id=?", "DELETE FROM billing_statement_line WHERE statement_id=?"} {
		require.Error(t, q.Exec(query, st.ID).Error)
	}
	// A committed fact with a historical service date is first observed after close.
	f.now = time.Date(2026, 11, 30, 23, 59, 59, 0, time.UTC)
	late = f.intent()
	require.NoError(t, f.calls.Start(f.ctx, late))
	f.now = time.Date(2026, 12, 1, 2, 0, 0, 0, time.UTC)
	require.NoError(t, f.calls.Finish(f.ctx, late.ID, billingSuccessResult()))
	next, err := svc.Generate(f.ctx, a, f.client.AppID, "2026-12")
	require.NoError(t, err)
	require.EqualValues(t, 5, next.TotalCents)
	detail, err := svc.Detail(f.ctx, a, next.ID, 0, 20)
	require.NoError(t, err)
	require.Len(t, detail.Lines, 1)
	require.Equal(t, "late_call_cash", detail.Lines[0].Kind)
	require.Equal(t, st.ID, detail.Lines[0].OriginalStatementID)
	original, err := svc.Detail(f.ctx, a, st.ID, 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 5, original.TotalCents)
}

func billingSuccessResult() gateway.CallResult {
	return gateway.CallResult{StatusCode: 200, Attempts: 1, Data: json.RawMessage(`{"status":"completed"}`)}
}
func billingFailedResult() gateway.CallResult {
	return gateway.CallResult{StatusCode: 500, Attempts: 1, ErrorCode: "10002"}
}

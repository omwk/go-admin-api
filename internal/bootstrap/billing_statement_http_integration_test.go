package bootstrap

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func TestBillingStatementHTTPIntegrationManagementIdempotencyAudit(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	f.now = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	billingStatementLogin(t, f)
	c := billingSucceed(t, f)
	in := map[string]any{"app_id": f.client.AppID, "month": "2026-11"}
	w, _ := f.manage(t, "POST", "/billing-statements:generate", in, "")
	require.Equal(t, 400, w.Code)
	w, body := f.manage(t, "POST", "/billing-statements:generate", in, "generate")
	require.Equal(t, 201, w.Code, w.Body.String())
	var st billing.Statement
	require.NoError(t, json.Unmarshal(body["data"], &st))
	require.EqualValues(t, 5, st.TotalCents)
	w, replay := f.manage(t, "POST", "/billing-statements:generate", in, "generate")
	require.Equal(t, 201, w.Code)
	require.JSONEq(t, string(body["data"]), string(replay["data"]))
	w, _ = f.manage(t, "POST", "/billing-statements:generate", map[string]any{"app_id": f.client.AppID, "month": "2026-10"}, "generate")
	require.Equal(t, 409, w.Code)
	path := "/billing-statements/" + strconv.FormatInt(st.ID, 10)
	w, body = f.manage(t, "GET", path, nil, "")
	require.Equal(t, 200, w.Code)
	require.NotContains(t, string(body["data"]), "tenant_id")
	w, _ = f.manage(t, "GET", "/billing-statements?page_size=101", nil, "")
	require.Equal(t, 400, w.Code)
	f.now = time.Date(2026, 12, 1, 1, 0, 0, 0, time.UTC)
	billingStatementLogin(t, f)
	billingCoverMonth(t, f, st.PeriodStart)
	w, _ = f.manage(t, "POST", path+"/issue", map[string]any{"version": st.Version + 1, "reason": "close"}, "issue-stale")
	require.Equal(t, 409, w.Code)
	w, _ = f.manage(t, "POST", path+"/issue", map[string]any{"version": st.Version, "reason": "close"}, "issue")
	require.Equal(t, 201, w.Code, w.Body.String())
	original := billingSettledTxn(t, f, c.CallID)
	adjust := billing.AdjustmentInput{OriginalTxnID: original.ID, Kind: "refund", AmountCents: 1, Reason: "partial refund", Evidence: "http-finance-case"}
	w, _ = f.manage(t, "POST", "/billing-adjustments", adjust, "refund")
	require.Equal(t, 201, w.Code, w.Body.String())
	w, body = f.manage(t, "GET", "/billing-adjustments", nil, "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, string(body["data"]), "http-finance-case")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("audit_log").Where("action='billing.statement.generate'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, q.Table("audit_log").Where("action='billing.statement.issue'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, q.Table("audit_log").Where("action='billing.adjustment.apply'").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestBillingStatementHTTPIntegrationJWTAndPermission(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	r := httptest.NewRequest("GET", "/api/v1/admin/billing-statements", nil)
	w, _ := f.serve(t, r)
	require.Equal(t, 401, w.Code)
	f.bearer = f.login(t).AccessToken
	w, _ = f.manage(t, "GET", "/billing-statements", nil, "")
	require.Equal(t, 403, w.Code)
	w, _ = f.manage(t, "POST", "/billing-adjustments", map[string]any{}, "no-perm")
	require.Equal(t, 403, w.Code)
}

func billingStatementLogin(t *testing.T, f *billingHTTPFixture) {
	t.Helper()
	tokens, err := f.identityFixture.service.Login(f.ctx, 1, identity.SeedAdminUsername, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	f.bearer = tokens.AccessToken
}

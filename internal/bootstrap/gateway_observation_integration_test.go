package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func TestGatewayObservabilityIntegrationListFiltersPrivacy(t *testing.T) {
	// BE-IDN-029 acceptance 1-4: strict filters, total/page agreement and no bodies.
	f := newGatewayHTTPFixture(t)
	for i, status := range []int{200, 204, 500, 403} {
		observationLog(t, f, 1, f.client.ID, f.route.ID, f.now.Add(-time.Duration(i+1)*time.Hour), status, (i+1)*10, fmt.Sprintf("exact-%d", i))
	}
	observationLog(t, f, 1, f.client.ID, f.route.ID, f.now.Add(-25*time.Hour), 200, 50, "old")
	page := observationPage(t, f, "")
	require.EqualValues(t, 4, page.Total)
	page = observationPage(t, f, fmt.Sprintf("?client_id=%d&route_id=%d&status=success&page=2&page_size=1", f.client.ID, f.route.ID))
	require.EqualValues(t, 2, page.Total)
	require.Len(t, page.List, 1)
	require.Equal(t, "exact-1", page.List[0].TraceID)
	require.Equal(t, f.client.Name, page.List[0].ClientName)
	require.Equal(t, f.route.Code, page.List[0].RouteCode)
	page = observationPage(t, f, "?status=failed")
	require.EqualValues(t, 2, page.Total)
	page = observationPage(t, f, "?trace_id=exact-2")
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, 500, page.List[0].StatusCode)
	require.Zero(t, observationPage(t, f, "?trace_id=exact").Total)
	require.EqualValues(t, 5, observationPage(t, f, "?"+observationRange(f)).Total)
	for _, query := range []string{"?from=" + f.now.Add(-31*24*time.Hour).Format(time.RFC3339), "?from=bad", "?page_size=101", "?page=0", "?status=other"} {
		w, _ := f.manage(t, "GET", "/api-calls"+query, nil, "")
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	t.Log("#1 total=4; client+route+success page2/size1=exact-1 total=2; failed=2; explicit range=5")
	t.Log("#2 default excludes 25h row; >30d and invalid filters=400; #3 exact trace=1/prefix=0; #4 body-free whitelist")
}

func TestGatewayObservabilityIntegrationSummarySQL(t *testing.T) {
	// BE-IDN-029 acceptance 5: interpolate ordered samples independently in SQL.
	f := newGatewayHTTPFixture(t)
	for i, status := range []int{200, 204, 500, 403} {
		observationLog(t, f, 1, f.client.ID, f.route.ID, f.now.Add(-time.Hour), status, (i+1)*10, "summary")
	}
	observationLog(t, f, 1, f.client.ID, f.route.ID, f.now.Add(-time.Hour), 0, 999, "processing-excluded")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	sql := `SELECT count(*) AS calls, sum(CASE WHEN status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END)::float8/count(*) AS rate, array_agg(latency_ms ORDER BY latency_ms) AS samples, sum(cost_cents) AS cost FROM api_call_log WHERE tenant_id=1 AND client_id=? AND finished_at IS NOT NULL`
	var actual struct {
		Calls   int64
		Rate    float64
		Samples string
		Cost    int64
	}
	require.NoError(t, q.Raw(sql, f.client.ID).Scan(&actual).Error)
	require.EqualValues(t, 4, actual.Calls)
	require.Equal(t, "{10,20,30,40}", actual.Samples)
	require.Equal(t, 0.5, actual.Rate)
	for _, group := range []string{"client", "route", "day"} {
		w, body := f.manage(t, "GET", "/api-calls/summary?group_by="+group, nil, "")
		require.Equal(t, 200, w.Code, w.Body.String())
		var summary gateway.CallSummary
		require.NoError(t, json.Unmarshal(body["data"], &summary))
		require.EqualValues(t, 4, summary.TotalCalls)
		require.Len(t, summary.Items, 1)
		item := summary.Items[0]
		require.EqualValues(t, 4, item.Calls)
		require.Equal(t, actual.Rate, item.SuccessRate)
		require.InDelta(t, 30+(40-30)*0.85, item.P95LatencyMS, 1e-9)
		require.Equal(t, actual.Cost, item.CostCents)
		t.Logf("#5 %s: key=%s calls=%d rate=%.2f P95=%.1f cost=%d", group, item.Key, item.Calls, item.SuccessRate, item.P95LatencyMS, item.CostCents)
	}
	t.Logf("SQL: %s; client_id=%d => calls=%d rate=%.2f samples=%s cost=%d; manual rank=1+(4-1)*.95=3.85; P95=30+10*.85=38.5", sql, f.client.ID, actual.Calls, actual.Rate, actual.Samples, actual.Cost)
}

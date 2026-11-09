package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func gatewayReadUsage(t *testing.T, f *gatewayHTTPFixture, path string) gateway.Usage {
	t.Helper()
	w, body := f.serve(t, f.sign(t, path, f.key.Secret))
	require.Equal(t, 200, w.Code, w.Body.String())
	var out gateway.Usage
	require.NoError(t, json.Unmarshal(body["data"], &out))
	return out
}

func TestGatewayUsageHTTPIntegrationObservationAndScope(t *testing.T) {
	// REQ-GW-007: the customer request ID does not deduplicate independent calls.
	f := newGatewayHTTPFixture(t)
	var ids []string
	for i := 0; i < 2; i++ {
		r := f.sign(t, f.publicPath(), f.key.Secret)
		r.Header.Set("X-Request-Id", "same-observation")
		w, _ := f.serve(t, r)
		require.Equal(t, 200, w.Code, w.Body.String())
		ids = append(ids, w.Header().Get("X-Call-Id"))
	}
	require.NotEqual(t, ids[0], ids[1])
	w, _ := f.serve(t, f.sign(t, f.publicPath()+"?unknown=1", f.key.Secret))
	require.Equal(t, 400, w.Code)
	gatewayAssertCall(t, f, w.Header().Get("X-Call-Id"), "failed", 0, 0)
	w, _ = f.serve(t, f.sign(t, f.publicPath(), f.randomValue(t)))
	require.Equal(t, 401, w.Code)
	usage := gatewayReadUsage(t, f, "/open/v1/usage?date="+f.now.UTC().Format("2006-01-02"))
	require.EqualValues(t, 3, usage.Calls)
	require.EqualValues(t, 2, usage.MeteredCalls)
	require.EqualValues(t, 2, usage.SucceededCalls)
	require.EqualValues(t, 1, usage.FailedCalls)
	require.Equal(t, f.now.UTC(), usage.AsOf)
	for _, path := range []string{"/open/v1/usage?client_id=1", "/open/v1/usage?date=invalid", "/open/v1/usage?date=2026-02-30", "/open/v1/usage?date="} {
		w, _ := f.serve(t, f.sign(t, path, f.key.Secret))
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	w, body := f.serve(t, f.sign(t, "/open/v1/balance", f.key.Secret))
	require.Equal(t, 503, w.Code)
	require.Equal(t, "10002", string(body["code"]))
	require.NotContains(t, body, "data")
	var err error
	f.client, f.key, err = f.life.Create(f.ctx, f.gatewayActor(), "own-usage", f.gatewayActor().UserID, nil)
	require.NoError(t, err)
	usage = gatewayReadUsage(t, f, "/open/v1/usage")
	require.Zero(t, usage.Calls)
	require.Zero(t, usage.MeteredCalls)
	status := gatewayUsageStatusWithoutAuth(f, "/open/v1/usage")
	require.Equal(t, http.StatusUnauthorized, status)
}

func gatewayUsageStatusWithoutAuth(f *gatewayHTTPFixture, path string) int {
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w.Code
}

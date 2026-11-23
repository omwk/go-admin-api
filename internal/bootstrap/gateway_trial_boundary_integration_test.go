package bootstrap

import (
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func TestGatewayObservabilityIntegrationTrialBoundary(t *testing.T) {
	// BE-IDN-029: no external flag may bypass billing, quota or metering.
	f := newGatewayHTTPFixture(t)
	request := f.sign(t, f.publicPath(), f.key.Secret)
	request.Header.Set("X-Gateway-Test", "true")
	request.Header.Set("X-Test", "true")
	w, _ := f.serve(t, request)
	require.Equal(t, 200, w.Code, w.Body.String())
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var meters, admitted int64
	require.NoError(t, q.Table("api_call_meter").Count(&meters).Error)
	require.EqualValues(t, 1, meters)
	require.NoError(t, q.Table("api_quota").Select("sum(admitted_count)").Scan(&admitted).Error)
	require.EqualValues(t, 2, admitted, "ordinary call consumes daily and monthly quota")
	var trials int64
	require.NoError(t, q.Table("api_call_log").Where("charge_source='manual_test'").Count(&trials).Error)
	require.Zero(t, trials)

	// Only route-matching paths are allowed; arbitrary absolute URLs are rejected.
	for _, path := range []string{"https://example.invalid/open/", "/api/v1/admin/users", "/open/v1/balance", f.publicPath() + "?test=true", "/open/v1/orders/%2E%2E/status"} {
		in := observationInput(f)
		in.Path = path
		w, _ := f.manage(t, "POST", resourcePath("api-clients", f.client.ID)+"/try", in, "")
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	input := observationInput(f)
	input.Query = map[string]string{"page": "1"}
	result := observationTrial(t, f, input)
	require.Equal(t, 400, result.StatusCode, "status capability rejects query parameters")
	require.Equal(t, 10001, result.ErrorCode)

	// A normal authenticated account with no gateway grants gets RBAC 403.
	f.bearer = f.identityFixture.login(t).AccessToken
	for _, route := range gateway.ObservationRoutes() {
		path := route.Path
		if route.Method == "POST" {
			path = fmt.Sprintf("/api-clients/%d/try", f.client.ID)
		}
		w, _ := f.manage(t, route.Method, path, observationInput(f), "")
		require.Equal(t, 403, w.Code, w.Body.String())
	}
	t.Log("external test headers retain meter=1 and daily/monthly quota=1 each; route path escape attempts=400; unprivileged management=403")
}

func TestGatewayObservabilityIntegrationTenantSuperBoundary(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'observation-other','observation-other') ON CONFLICT(id) DO NOTHING").Error)
	seed, err := identity.NewSeedService(&seedStore{authStore: newAuthStore(f.database)})
	require.NoError(t, err)
	require.NoError(t, seed.Run(f.ctx, 2, f.password))
	tokens, err := f.identityFixture.service.Login(f.ctx, 2, identity.SeedAdminUsername, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	f.bearer = tokens.AccessToken
	observationLog(t, f, 1, f.client.ID, f.route.ID, f.now.Add(-time.Hour), 200, 10, "platform-only")
	require.Zero(t, observationPage(t, f, "?trace_id=platform-only").Total)
	w, _ := f.manage(t, "POST", resourcePath("api-clients", f.client.ID)+"/try", observationInput(f), "")
	require.Equal(t, 404, w.Code, w.Body.String())
	t.Log("tenant 2 super administrator cannot observe or try tenant 1 clients")
}

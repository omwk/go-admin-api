package bootstrap

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func TestGatewayQuotaHTTPIntegrationHeadersAndNoReservationOnDenial(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	w, _ := f.manage(t, "POST", fmt.Sprintf("/api-clients/%d/routes", f.client.ID), map[string]any{
		"route_id": f.route.ID, "status": "active", "quota_daily": 1, "quota_monthly": 0,
	}, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	w, _ = f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
	require.Equal(t, 200, w.Code, w.Body.String())
	w, body := f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
	require.Equal(t, 429, w.Code, w.Body.String())
	require.Equal(t, "10005", string(body["code"]))
	require.JSONEq(t, `{"limit_type":"daily_quota"}`, string(body["details"]))
	retry, err := strconv.ParseInt(w.Header().Get("Retry-After"), 10, 64)
	require.NoError(t, err)
	require.Greater(t, retry, int64(0))
	require.LessOrEqual(t, retry, int64(86400))
	gatewayAssertCall(t, f, w.Header().Get("X-Call-Id"), "failed", 0, 0)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var counts []int64
	require.NoError(t, q.Table("api_quota").Order("quota_key").Pluck("admitted_count", &counts).Error)
	require.Equal(t, []int64{1, 1}, counts)
	usage := gatewayReadUsage(t, f, "/open/v1/usage")
	require.EqualValues(t, 2, usage.Calls)
	require.EqualValues(t, 1, usage.MeteredCalls)
	var details map[string]any
	require.NoError(t, json.Unmarshal(body["details"], &details))
	require.Len(t, details, 1)
}

func TestGatewayRateHTTPIntegrationRetryAfterAndOtherApplication(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	f.server.SetTime(f.now)
	nonces, err := newGatewayNonces(f.ctx, config.Redis{Addr: f.server.Addr(), PoolSize: 4})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, nonces.Close()) })
	limits, err := newGatewayLimiter(nonces.client, config.Gateway{GlobalQPS: 100, GlobalBurst: 100, ClientQPS: 1, ClientBurst: 1, RouteQPS: 100, RouteBurst: 100})
	require.NoError(t, err)
	f.handler = gatewayMeteredHTTP(t, f, nil, limits)
	w, _ := f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
	require.Equal(t, 200, w.Code, w.Body.String())
	w, body := f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
	require.Equal(t, 429, w.Code, w.Body.String())
	require.Equal(t, "10005", string(body["code"]))
	require.Equal(t, "1", w.Header().Get("Retry-After"))
	require.Equal(t, "1", w.Header().Get("X-RateLimit-Limit"))
	require.JSONEq(t, `{"limit_type":"client"}`, string(body["details"]))
	gatewayAssertCall(t, f, w.Header().Get("X-Call-Id"), "failed", 0, 0)
	f.client, f.key, err = f.life.Create(f.ctx, f.gatewayActor(), "rate-isolated-app", f.gatewayActor().UserID, nil)
	require.NoError(t, err)
	require.NoError(t, f.gate.GrantRoute(f.ctx, f.gatewayActor(), f.client.ID, f.route.ID, "active", nil, gateway.QuotaLimits{}))
	f.grant, err = f.resources.Grant(f.ctx, f.gatewayActor(), f.client.ID, f.orderID)
	require.NoError(t, err)
	w, _ = f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
	require.Equal(t, 200, w.Code, w.Body.String())
}

func TestGatewayAdmissionHTTPIntegrationDependenciesFailClosed(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	f.handler = gatewayMeteredHTTP(t, f, nil, gatewayUnavailableLimiter{})
	w, body := f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
	require.Equal(t, 503, w.Code, w.Body.String())
	require.Equal(t, "10002", string(body["code"]))
	require.NotContains(t, body, "data")
	gatewayAssertCall(t, f, w.Header().Get("X-Call-Id"), "failed", 0, 0)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var reservations int64
	require.NoError(t, q.Table("api_quota").Count(&reservations).Error)
	require.Zero(t, reservations)
	failed, err := gateway.NewCallService(&gatewayMeterFailure{f.gate}, func() time.Time { return f.now }, gateway.CallPolicy{Timeout: time.Second, MaxAttempts: 2, HashKey: shopRandomBytes(t, 32)})
	require.NoError(t, err)
	f.handler = gatewayMeteredHTTP(t, f, nil, nil, failed)
	w, body = f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
	require.Equal(t, 503, w.Code, w.Body.String())
	require.NotContains(t, body, "data")
	gatewayAssertCall(t, f, w.Header().Get("X-Call-Id"), "processing", 0, 0)
}

package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func seededGatewayUsage(t *testing.T) *gatewayHTTPFixture {
	t.Helper()
	f := newGatewayHTTPFixture(t)
	require.NoError(t, SeedGatewayRoutes(f.ctx, f.database, f.gatewayActor().TenantID))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	f.route = gateway.Route{}
	require.NoError(t, q.Table("api_route").Select(gatewayRouteColumns).
		Where("tenant_id=? AND code=?", f.gatewayActor().TenantID, "usage.read").Take(&f.route).Error)
	require.NoError(t, f.gate.GrantRoute(f.ctx, f.gatewayActor(), f.client.ID, f.route.ID, "active", nil, gateway.QuotaLimits{}))
	return f
}

func TestGatewaySeedIntegrationUsageTrial(t *testing.T) {
	// BE-IDN-030 §3.5: actual management HTTP -> HMAC -> usage -> durable metadata.
	f := seededGatewayUsage(t)
	in := gateway.TryInput{RouteID: f.route.ID, Method: "GET", Path: "/open/v1/usage", Body: json.RawMessage(`null`)}
	w, body := f.manage(t, "POST", resourcePath("api-clients", f.client.ID)+"/try", in, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var result gateway.TryResult
	require.NoError(t, json.Unmarshal(body["data"], &result))
	require.Equal(t, 200, result.StatusCode)
	require.Zero(t, result.ErrorCode)
	require.True(t, result.Test)
	require.False(t, result.Billing.Billed)
	require.Equal(t, "free", result.Billing.Mode)
	require.NotEmpty(t, result.CallID)
	data, err := json.Marshal(result.Response)
	require.NoError(t, err)
	var usage gateway.Usage
	require.NoError(t, json.Unmarshal(data, &usage))
	require.Equal(t, f.now.UTC().Format("2006-01-02"), usage.Date)
	require.Equal(t, f.now.UTC(), usage.AsOf)
	call, err := f.gate.Call(f.ctx, result.CallID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", call.State)
	require.Equal(t, "manual_test", call.ChargeSource)
	require.JSONEq(t, `{}`, string(call.Result))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, table := range []string{"api_call_meter", "call_charge", "wallet_txn", "unit_txn", "api_quota"} {
		var count int64
		require.NoError(t, q.Table(table).Count(&count).Error)
		require.Zero(t, count, table)
	}
	var logs int64
	require.NoError(t, q.Table("api_call_log").Where("call_id=? AND charge_source='manual_test' AND state='succeeded'", result.CallID).Count(&logs).Error)
	require.EqualValues(t, 1, logs)
	t.Logf("#5 POST /api/v1/admin/api-clients/%d/try request={route_id:%d,method:GET,path:/open/v1/usage,body:null}", f.client.ID, f.route.ID)
	t.Logf("#5 HTTP %d response=%s", w.Code, w.Body.String())
	t.Log("usage trial log=manual_test/succeeded, stored result={}; meter/charge/wallet/unit/quota rows=0")
}

func TestGatewaySeedIntegrationUsageTrialGates(t *testing.T) {
	f := seededGatewayUsage(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, tc := range []struct {
		name, table, predicate, column, value, restore string
		id                                             int64
		status, code                                   int
	}{
		{"ungranted", "api_client_route", "route_id=?", "status", "suspended", "active", f.route.ID, 403, 80104},
		{"disabled", "api_route", "id=?", "status", "disabled", "enabled", f.route.ID, 404, 80201},
		{"private", "api_route", "id=?", "visibility", "private", "public", f.route.ID, 404, 80201},
		{"client suspended", "api_client", "id=?", "status", "suspended", "active", f.client.ID, 401, 80101},
		{"key revoked", "api_client_secret", "id=?", "status", "revoked", "active", f.key.ID, 401, 80102},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, q.Table(tc.table).Where(tc.predicate, tc.id).Update(tc.column, tc.value).Error)
			out := observationTrial(t, f, gateway.TryInput{RouteID: f.route.ID, Path: f.route.Path})
			require.Equal(t, tc.status, out.StatusCode)
			require.Equal(t, tc.code, out.ErrorCode)
			require.NoError(t, q.Table(tc.table).Where(tc.predicate, tc.id).Update(tc.column, tc.restore).Error)
		})
	}
	for _, path := range []string{"/api/v1/admin/users", "/open/v1/balance", "https://example.invalid/open/v1/usage"} {
		w, _ := f.manage(t, "POST", resourcePath("api-clients", f.client.ID)+"/try", gateway.TryInput{RouteID: f.route.ID, Path: path}, "")
		require.Equal(t, 400, w.Code)
	}
	out := observationTrial(t, f, gateway.TryInput{RouteID: f.route.ID, Query: map[string]string{"date": "invalid"}})
	require.Equal(t, 400, out.StatusCode)
	require.Equal(t, 10001, out.ErrorCode)
	t.Log("usage trial: suspended grant=403; disabled/private=404; suspended client/revoked key=401; path escape=400; invalid date=400")
}

func TestGatewaySeedIntegrationUsageTrialQuota(t *testing.T) {
	f := seededGatewayUsage(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, kind := range []string{"daily", "monthly"} {
		require.NoError(t, q.Table("api_client_route").Where("route_id=?", f.route.ID).Update("quota_"+kind, 1).Error)
		period := f.now.Format("2006-01-02")
		if kind == "monthly" {
			period = f.now.Format("2006-01")
		}
		key := fmt.Sprintf("quota:%d:%s", f.client.ID, period)
		require.NoError(t, q.Table("api_quota").Create(map[string]any{"tenant_id": f.client.TenantID, "client_id": f.client.ID,
			"quota_key": key, "admitted_count": 1, "reset_at": f.now.Add(24 * time.Hour)}).Error)
		out := observationTrial(t, f, gateway.TryInput{RouteID: f.route.ID})
		require.Equal(t, 429, out.StatusCode)
		require.Equal(t, 10005, out.ErrorCode)
		var used int64
		require.NoError(t, q.Table("api_quota").Select("admitted_count").Where("quota_key=?", key).Scan(&used).Error)
		require.EqualValues(t, 1, used)
		require.NoError(t, q.Table("api_quota").Where("quota_key=?", key).Delete(nil).Error)
		require.NoError(t, q.Table("api_client_route").Where("route_id=?", f.route.ID).Update("quota_"+kind, 0).Error)
	}
	t.Log("usage trial enforces daily/monthly quota exhaustion: 429/10005; admitted_count unchanged")
}

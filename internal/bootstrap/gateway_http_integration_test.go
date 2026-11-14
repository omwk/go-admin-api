package bootstrap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func TestGatewayHTTPIntegrationPublicContractAndMarkers(t *testing.T) {
	// REQ-GW-001/003/006: a real normalized order produces only the reviewed DTO.
	f := newGatewayHTTPFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	marker := f.randomValue(t)
	require.NoError(t, q.Table("sales_order").Where("id=?", f.orderID).Updates(map[string]any{"external_order_id": marker, "platform_status": marker, "total_amount_cents": 987654321}).Error)
	require.NoError(t, q.Table("order_item").Where("order_id=?", f.orderID).Update("title", marker).Error)
	w, body := f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
	require.Equal(t, 200, w.Code, w.Body.String())
	var data map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body["data"], &data))
	require.ElementsMatch(t, []string{"id", "order_no", "status", "updated_at"}, mapKeys(data))
	require.JSONEq(t, fmt.Sprintf(`"%s"`, f.grant.PublicID), string(data["id"]))
	for _, forbidden := range []string{marker, "tenant_id", "shop_id", "platform_id", "external_order_id", "owner_user_id", "raw_id", "receiver", "cost_cents", "987654321"} {
		require.NotContains(t, w.Body.String(), forbidden)
	}
	require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
	f.noLeaks(t, "application logs", f.logs.text())
	f.noLeaks(t, "SQL logs", f.sqlLogs.text())
	w, _ = f.serve(t, f.sign(t, "/open/v1/admin/users", f.key.Secret))
	require.Equal(t, 404, w.Code)
	w, _ = f.manage(t, "GET", "/orders", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var calls int64
	require.NoError(t, q.Table("api_call_log").Count(&calls).Error)
	require.EqualValues(t, 1, calls)
	var units int64
	require.NoError(t, q.Table("api_call_meter").Select("coalesce(sum(units),0)").Scan(&units).Error)
	require.EqualValues(t, 1, units)
}

func TestGatewayHTTPIntegrationDoubleGateAndAuthorization(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, change := range []map[string]any{{"status": "disabled"}, {"visibility": "private"}, {"capability": "admin.secret"}, {"billing_mode": "per_call"}} {
		require.NoError(t, q.Table("api_route").Where("id=?", f.route.ID).Updates(change).Error)
		w, body := f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
		require.Equal(t, 404, w.Code, w.Body.String())
		require.Equal(t, "80201", string(body["code"]))
		require.JSONEq(t, `"接口不可用"`, string(body["message"]))
		require.NoError(t, q.Table("api_route").Where("id=?", f.route.ID).Updates(map[string]any{"status": "enabled", "visibility": "public", "capability": "order.status", "billing_mode": "free"}).Error)
	}
	require.NoError(t, f.gate.GrantRoute(f.ctx, f.gatewayActor(), f.client.ID, f.route.ID, "suspended", nil, gateway.QuotaLimits{}))
	w, body := f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
	require.Equal(t, 403, w.Code)
	require.Equal(t, "80104", string(body["code"]))
	require.NoError(t, f.gate.GrantRoute(f.ctx, f.gatewayActor(), f.client.ID, f.route.ID, "active", nil, gateway.QuotaLimits{}))
	require.NoError(t, f.resources.Revoke(f.ctx, f.gatewayActor(), f.client.ID, f.grant.ID, f.grant.Version))
	w, body = f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
	require.Equal(t, 404, w.Code)
	require.Equal(t, "10003", string(body["code"]))
	w, body = f.serve(t, f.sign(t, "/open/v1/orders/"+ulid.Make().String()+"/status", f.key.Secret))
	require.Equal(t, 404, w.Code)
	require.Equal(t, "10003", string(body["code"]))
}

func TestGatewayHTTPIntegrationUniformAuthenticationAndRevocation(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	bad := f.sign(t, f.publicPath(), f.randomValue(t))
	expired := f.sign(t, f.publicPath(), f.key.Secret)
	expired.Header.Set("X-Timestamp", fmt.Sprint(f.now.Unix()-301))
	replay := f.sign(t, f.publicPath(), f.key.Secret)
	w, _ := f.serve(t, replay)
	require.Equal(t, 200, w.Code)
	for _, r := range []*http.Request{bad, expired, replay} {
		w, body := f.serve(t, r)
		require.Equal(t, 401, w.Code, w.Body.String())
		require.JSONEq(t, `"应用凭证不可用"`, string(body["message"]))
	}
	require.NoError(t, f.life.Revoke(f.ctx, f.gatewayActor(), f.client.ID, f.key.ID))
	w, body := f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
	require.Equal(t, 401, w.Code)
	require.Equal(t, "80102", string(body["code"]))
	require.JSONEq(t, `"应用凭证不可用"`, string(body["message"]))
}

func TestGatewayHTTPIntegrationResourceCeilingAndLiveOwnerScope(t *testing.T) {
	for _, name := range []string{"other application", "other tenant", "expired grant", "owner permission removed", "shop transferred", "resource removed"} {
		t.Run(name, func(t *testing.T) {
			f := newGatewayHTTPFixture(t)
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			switch name {
			case "other application":
				var err error
				f.client, f.key, err = f.life.Create(f.ctx, f.gatewayActor(), "different-app", f.gatewayActor().UserID, nil)
				require.NoError(t, err)
				require.NoError(t, f.gate.GrantRoute(f.ctx, f.gatewayActor(), f.client.ID, f.route.ID, "active", nil, gateway.QuotaLimits{}))
			case "other tenant":
				require.NoError(t, q.Table("sales_order").Where("id=?", f.orderID).Update("tenant_id", 2).Error)
			case "expired grant":
				require.NoError(t, q.Table("api_client_resource_grant").Where("id=?", f.grant.ID).Update("expires_at", time.Now().Add(-time.Minute)).Error)
			case "owner permission removed":
				require.NoError(t, q.Table("sys_user").Where("id=?", f.gatewayActor().UserID).Update("is_super", false).Error)
				require.NoError(t, q.Table("sys_role_permission").Where("role_id IN (SELECT id FROM sys_role WHERE code=?)", identity.SuperAdminRoleCode).Delete(map[string]any{}).Error)
			case "shop transferred":
				ordinaryScopeActor(t, f.identityFixture, f.gatewayActor().UserID)
				require.NoError(t, q.Table("sys_role").Where("code=?", identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
				require.NoError(t, q.Table("shop").Where("id=?", f.shop.ID).Update("owner_user_id", f.user.ID).Error)
			case "resource removed":
				require.NoError(t, q.Table("sales_order").Where("id=?", f.orderID).Update("deleted_at", time.Now()).Error)
			}
			w, body := f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
			require.Equal(t, 404, w.Code, w.Body.String())
			require.Equal(t, "10003", string(body["code"]))
			require.NotContains(t, body, "data")
		})
	}
}

func TestGatewayHTTPIntegrationManagementAndSecretReceipts(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	path := fmt.Sprintf("/api-clients/%d/secrets", f.client.ID)
	key := f.randomValue(t)
	w, body := f.manage(t, "POST", path, map[string]any{"overlap_seconds": 3600}, key)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
	var issued gateway.IssuedSecret
	require.NoError(t, json.Unmarshal(body["data"], &issued))
	require.NotEmpty(t, issued.Secret)
	f.markers = append(f.markers, issued.Secret, f.key.Secret)
	w, body = f.manage(t, "POST", path, map[string]any{"overlap_seconds": 3600}, key)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.JSONEq(t, `{"delivered":true}`, string(body["data"]))
	w, _ = f.manage(t, "GET", path, nil, "")
	require.Equal(t, 200, w.Code)
	f.noLeaks(t, "secret list", w.Body.String())
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var receipts []string
	require.NoError(t, q.Table("idempotency_key").Select("response_body").Pluck("response_body", &receipts).Error)
	for _, value := range receipts {
		f.noLeaks(t, "idempotency receipts", value)
	}
	w, _ = f.manage(t, "DELETE", path+"/"+fmt.Sprint(issued.ID), nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	w, _ = f.serve(t, f.sign(t, f.publicPath(), issued.Secret))
	require.Equal(t, 401, w.Code)
	request := f.sign(t, "/api/v1/admin/api-clients", f.key.Secret)
	w, _ = f.serve(t, request)
	require.Equal(t, 401, w.Code)
}

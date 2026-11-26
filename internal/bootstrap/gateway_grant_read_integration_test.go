package bootstrap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

type gatewayGrantView struct {
	ID           int64      `json:"id"`
	RouteID      int64      `json:"route_id"`
	RouteCode    string     `json:"route_code"`
	RouteName    string     `json:"route_name"`
	Method       string     `json:"method"`
	Path         string     `json:"path"`
	Status       string     `json:"status"`
	QuotaDaily   int64      `json:"quota_daily"`
	QuotaMonthly int64      `json:"quota_monthly"`
	GrantedAt    time.Time  `json:"granted_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	Version      int        `json:"version"`
}

func TestGatewayGrantReadIntegrationClientAndSortedList(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()

	var firstRouteID int64
	require.NoError(t, q.Raw(`INSERT INTO api_route
		(tenant_id,code,name,capability,method,path,route_group,visibility,status,billing_mode,created_by)
		VALUES (1,'alpha.usage','Alpha usage','usage.read','HEAD','/open/v1/alpha-usage','usage','private','disabled','free',?)
		RETURNING id`, f.gatewayActor().UserID).Scan(&firstRouteID).Error)
	require.NoError(t, f.gate.GrantRoute(f.ctx, f.gatewayActor(), f.client.ID, firstRouteID, "active", nil, gateway.QuotaLimits{Daily: 100, Monthly: 1000}))
	require.NoError(t, q.Table("api_client_route").Where("tenant_id=? AND client_id=? AND route_id=?", 1, f.client.ID, f.route.ID).
		Updates(map[string]any{"quota_daily": 20, "quota_monthly": 200}).Error)

	w, body := f.manage(t, http.MethodGet, fmt.Sprintf("/api-clients/%d/routes", f.client.ID), nil, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	validateOpenAPIResponse(t, observationContract(t), "/api/v1/admin/api-clients/{id}/routes", "get", w.Code, w.Body.Bytes())
	var grants []gatewayGrantView
	require.NoError(t, json.Unmarshal(body["data"], &grants))
	require.Len(t, grants, 2)
	require.Equal(t, []string{"alpha.usage", "order.status"}, []string{grants[0].RouteCode, grants[1].RouteCode})
	require.Equal(t, firstRouteID, grants[0].RouteID)
	require.Equal(t, "Alpha usage", grants[0].RouteName)
	require.Equal(t, "HEAD", grants[0].Method)
	require.Equal(t, "/open/v1/alpha-usage", grants[0].Path)
	require.EqualValues(t, 100, grants[0].QuotaDaily)
	require.EqualValues(t, 1000, grants[0].QuotaMonthly)
	require.False(t, grants[0].GrantedAt.IsZero())
	require.Nil(t, grants[0].ExpiresAt)
	require.Equal(t, 1, grants[0].Version)
	require.EqualValues(t, 20, grants[1].QuotaDaily)
	require.EqualValues(t, 200, grants[1].QuotaMonthly)

	w, body = f.manage(t, http.MethodGet, fmt.Sprintf("/api-clients/%d", f.client.ID), nil, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	validateOpenAPIResponse(t, observationContract(t), "/api/v1/admin/api-clients/{id}", "get", w.Code, w.Body.Bytes())
	var client struct {
		ID          int64    `json:"id"`
		AppID       string   `json:"app_id"`
		Name        string   `json:"name"`
		OwnerUserID int64    `json:"owner_user_id"`
		Status      string   `json:"status"`
		Version     int      `json:"version"`
		IPWhitelist []string `json:"ip_whitelist"`
	}
	require.NoError(t, json.Unmarshal(body["data"], &client))
	require.Equal(t, f.client.ID, client.ID)
	require.Equal(t, f.client.AppID, client.AppID)
	require.Equal(t, f.client.Name, client.Name)
	require.Equal(t, f.client.OwnerUserID, client.OwnerUserID)
	require.Equal(t, f.client.Status, client.Status)
	require.Equal(t, f.client.Version, client.Version)
	require.Empty(t, client.IPWhitelist)
	require.ElementsMatch(t, []string{"id", "app_id", "name", "owner_user_id", "status", "version", "ip_whitelist"}, mapKeys(mustJSONMap(t, body["data"])))

	emptyClient, _, err := f.life.Create(f.ctx, f.gatewayActor(), "empty-grant-reader", f.gatewayActor().UserID, nil)
	require.NoError(t, err)
	w, body = f.manage(t, http.MethodGet, fmt.Sprintf("/api-clients/%d/routes", emptyClient.ID), nil, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var empty []gatewayGrantView
	require.NoError(t, json.Unmarshal(body["data"], &empty))
	require.NotNil(t, empty)
	require.Empty(t, empty)
	t.Log("BE-IDN-033 §3 #1-3: route metadata, quota, empty list, and ascending route_code verified; client detail contains no key material")
}

func TestGatewayGrantReadIntegrationRevokeAuditAndTenantScope(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	routeGrantID := gatewayRouteGrantID(t, f, f.route.ID)
	path := fmt.Sprintf("/api-clients/%d/routes/%d", f.client.ID, routeGrantID)
	w := gatewayAdminDelete(t, f, path)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.Empty(t, w.Body.String())
	validateOpenAPIResponse(t, observationContract(t), "/api/v1/admin/api-clients/{id}/routes/{grant_id}", "delete", w.Code, w.Body.Bytes())
	w, body := f.manage(t, http.MethodGet, fmt.Sprintf("/api-clients/%d/routes", f.client.ID), nil, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var grants []gatewayGrantView
	require.NoError(t, json.Unmarshal(body["data"], &grants))
	require.Empty(t, grants)
	t.Logf("#4 list after revoke=%s", string(body["data"]))
	assertGatewayGrantRevokeAudit(t, f)
	w = gatewayAdminDelete(t, f, path)
	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	t.Log("BE-IDN-033 §3 #4-6: DELETE returned 204, the grant disappeared, the audit carries client_id/route_id, and duplicate revoke returned 404")

	_, foreignClient, foreignGrant := createForeignGatewayGrant(t, f)
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, fmt.Sprintf("/api-clients/%d", foreignClient)},
		{http.MethodGet, fmt.Sprintf("/api-clients/%d/routes", foreignClient)},
		{http.MethodDelete, fmt.Sprintf("/api-clients/%d/routes/%d", foreignClient, foreignGrant)},
	} {
		var response map[string]json.RawMessage
		if request.method == http.MethodDelete {
			w = gatewayAdminDelete(t, f, request.path)
		} else {
			w, response = f.manage(t, request.method, request.path, nil, "")
		}
		require.Equal(t, http.StatusNotFound, w.Code, request.method+" "+request.path)
		if request.method == http.MethodGet && request.path == fmt.Sprintf("/api-clients/%d", foreignClient) {
			validateOpenAPIResponse(t, observationContract(t), "/api/v1/admin/api-clients/{id}", "get", w.Code, w.Body.Bytes())
			t.Logf("#8 GET /api-clients/%d cross-tenant status=%d code=%s", foreignClient, w.Code, response["code"])
		}
	}
	w, body = f.manage(t, http.MethodGet, fmt.Sprintf("/api-clients/%d", f.client.ID), nil, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	validateOpenAPIResponse(t, observationContract(t), "/api/v1/admin/api-clients/{id}", "get", w.Code, w.Body.Bytes())
	var returned map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body["data"], &returned))
	require.Equal(t, f.client.AppID, stringValue(t, returned["app_id"]))
	t.Log("BE-IDN-033 §3 #7-8: foreign-tenant client and grant operations return 404; owned client detail is returned")
}

func TestGatewayGrantReadIntegrationDeletedRouteFallback(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	routeGrantID := gatewayRouteGrantID(t, f, f.route.ID)
	deleted := false
	restore := func() {
		if deleted {
			require.NoError(t, q.Table("api_route").Create(&f.route).Error)
			deleted = false
		}
	}
	t.Cleanup(restore)

	result := q.Exec("DELETE FROM api_route WHERE tenant_id=? AND id=?", f.route.TenantID, f.route.ID)
	require.NoError(t, result.Error)
	require.EqualValues(t, 1, result.RowsAffected)
	deleted = true
	w, body := f.manage(t, http.MethodGet, fmt.Sprintf("/api-clients/%d/routes", f.client.ID), nil, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var grants []gatewayGrantView
	require.NoError(t, json.Unmarshal(body["data"], &grants))
	require.Len(t, grants, 1)
	require.Equal(t, f.route.ID, grants[0].RouteID)
	require.Equal(t, "#"+fmt.Sprint(f.route.ID), grants[0].RouteCode)
	require.Empty(t, grants[0].RouteName)
	require.Empty(t, grants[0].Method)
	require.Empty(t, grants[0].Path)
	var grantRows int64
	require.NoError(t, q.Table("api_client_route").Where("tenant_id=? AND client_id=? AND id=?", 1, f.client.ID, routeGrantID).Count(&grantRows).Error)
	require.EqualValues(t, 1, grantRows)
	restore()
	var restoredRoutes int64
	require.NoError(t, q.Table("api_route").Where("tenant_id=? AND id=?", f.route.TenantID, f.route.ID).Count(&restoredRoutes).Error)
	require.EqualValues(t, 1, restoredRoutes)
	t.Logf("BE-IDN-033 §3 #9: physically deleted api_route %d; grant remained readable as route_code=%q; route row restored", f.route.ID, grants[0].RouteCode)
}

func gatewayAdminDelete(t *testing.T, f *gatewayHTTPFixture, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/admin"+path, nil)
	r.Header.Set("Authorization", "Bearer "+f.bearer)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func gatewayRouteGrantID(t *testing.T, f *gatewayHTTPFixture, routeID int64) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var id int64
	require.NoError(t, q.Table("api_client_route").Select("id").Where("tenant_id=? AND client_id=? AND route_id=?", f.gatewayActor().TenantID, f.client.ID, routeID).Take(&id).Error)
	return id
}

func assertGatewayGrantRevokeAudit(t *testing.T, f *gatewayHTTPFixture) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var event struct {
		Action       string
		ResourceType string
		ResourceID   string
		BeforeJSON   string
	}
	require.NoError(t, q.Raw(`SELECT action,resource_type,resource_id,"before"::text AS before_json FROM audit_log
		WHERE tenant_id=? AND action='gateway.route.revoke' ORDER BY id DESC LIMIT 1`, f.gatewayActor().TenantID).Scan(&event).Error)
	require.Equal(t, "gateway.route.revoke", event.Action)
	require.Equal(t, "api_client_route", event.ResourceType)
	require.Equal(t, fmt.Sprintf("%d:%d", f.client.ID, f.route.ID), event.ResourceID)
	var before map[string]any
	require.NoError(t, json.Unmarshal([]byte(event.BeforeJSON), &before))
	require.Equal(t, float64(f.client.ID), before["client_id"])
	require.Equal(t, float64(f.route.ID), before["route_id"])
	t.Logf("#5 audit action=%s resource_type=%s resource_id=%s before=%s", event.Action, event.ResourceType, event.ResourceID, event.BeforeJSON)
}

func createForeignGatewayGrant(t *testing.T, f *gatewayHTTPFixture) (int64, int64, int64) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var tenantID, clientID, routeID, grantID int64
	require.NoError(t, q.Raw("INSERT INTO tenant(code,name) VALUES (?,?) RETURNING id", "gateway-foreign-"+ulid.Make().String(), "Foreign gateway").Scan(&tenantID).Error)
	require.NoError(t, q.Raw(`INSERT INTO api_client(tenant_id,app_id,name,owner_user_id)
		VALUES (?,?,?,?) RETURNING id`, tenantID, ulid.Make().String(), "foreign-client", f.gatewayActor().UserID).Scan(&clientID).Error)
	require.NoError(t, q.Raw(`INSERT INTO api_route
		(tenant_id,code,name,capability,method,path,route_group,visibility,status,billing_mode,created_by)
		VALUES (?,?,?,?,?,?,?,?,?,?,?) RETURNING id`, tenantID, "foreign.route", "Foreign route", "usage.read", "GET", "/open/v1/foreign-route", "usage", "private", "disabled", "free", f.gatewayActor().UserID).Scan(&routeID).Error)
	require.NoError(t, q.Raw(`INSERT INTO api_client_route(tenant_id,client_id,route_id,granted_by)
		VALUES (?,?,?,?) RETURNING id`, tenantID, clientID, routeID, f.gatewayActor().UserID).Scan(&grantID).Error)
	return tenantID, clientID, grantID
}

func mustJSONMap(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var value map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &value))
	return value
}

func stringValue(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value string
	require.NoError(t, json.Unmarshal(raw, &value))
	return value
}

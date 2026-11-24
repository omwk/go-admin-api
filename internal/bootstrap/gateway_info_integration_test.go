package bootstrap

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

const gatewayInfoContractPath = "/api/v1/admin/gateway/info"

func TestGatewayInfoIntegrationReadOnlyPermissionAndContract(t *testing.T) {
	// BE-IDN-032 §4: read access requires gateway:client:list and returns deployment settings.
	f := newAdminFixture(t)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	cfg := &config.Config{
		Tenant: config.Tenant{PlatformID: 1},
		Crypto: config.Crypto{MasterKey: base64.StdEncoding.EncodeToString(shopRandomBytes(t, 32)), KeyVersion: 1},
		Gateway: config.Gateway{
			PublicBaseURL:  "https://api.example:8443",
			AllowedOrigins: []string{"https://admin.example", "http://localhost:3000"},
		},
	}
	require.NoError(t, registerGatewayRoutes(f.router, f.identityFixture.service, profiles, f.database, cfg))
	doc := observationContract(t)
	tokens := f.login(t)
	for _, tc := range []struct {
		bearer string
		status int
	}{{"", 401}, {tokens.AccessToken, 403}} {
		w := gatewayInfoRequest(f.router, http.MethodGet, tc.bearer)
		require.Equal(t, tc.status, w.Code, w.Body.String())
		validateOpenAPIResponse(t, doc, gatewayInfoContractPath, "get", w.Code, w.Body.Bytes())
	}

	grantGatewayInfoPermission(t, f)
	w := gatewayInfoRequest(f.router, http.MethodGet, tokens.AccessToken)
	require.Equal(t, 200, w.Code, w.Body.String())
	validateOpenAPIResponse(t, doc, gatewayInfoContractPath, "get", w.Code, w.Body.Bytes())
	require.Contains(t, w.Body.String(), `"data":{"public_base_url":"https://api.example:8443","allowed_origins":["https://admin.example","http://localhost:3000"]}`)
	post := gatewayInfoRequest(f.router, http.MethodPost, tokens.AccessToken)
	require.Equal(t, 405, post.Code, post.Body.String())

	router := identityRouter(t, f.identityFixture)
	cfg.Gateway.AllowedOrigins = nil
	require.NoError(t, registerGatewayRoutes(router, f.identityFixture.service, profiles, f.database, cfg))
	w = gatewayInfoRequest(router, http.MethodGet, tokens.AccessToken)
	require.Equal(t, 200, w.Code, w.Body.String())
	validateOpenAPIResponse(t, doc, gatewayInfoContractPath, "get", w.Code, w.Body.Bytes())
	require.Contains(t, w.Body.String(), `"allowed_origins":[]`)
}

func gatewayInfoRequest(router http.Handler, method, bearer string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, gatewayInfoContractPath, nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func grantGatewayInfoPermission(t *testing.T, f *adminFixture) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	tenant := f.user.TenantID
	role := model.Role{TenantID: tenant, Code: "gateway-info-reader", Name: "Gateway Info Reader", DataScope: "self", Status: "active"}
	require.NoError(t, q.Create(&role).Error)
	var point model.Permission
	require.NoError(t, q.Select("id").Where("tenant_id = ? AND code = ?", tenant, permission.GatewayClientList).Take(&point).Error)
	require.NoError(t, q.Create(&model.RolePermission{TenantID: tenant, RoleID: role.ID, PermissionID: point.ID}).Error)
	require.NoError(t, q.Create(&model.UserRole{TenantID: tenant, RoleID: role.ID, UserID: f.user.ID}).Error)
}

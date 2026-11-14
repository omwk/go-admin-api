package bootstrap

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func addLoginTenant(t *testing.T, f *identityFixture, code string) model.User {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	tenant := model.Tenant{Code: code, Name: code, Status: "active", Settings: json.RawMessage("{}")}
	require.NoError(t, q.Create(&tenant).Error)
	dept := model.Department{TenantID: tenant.ID, Name: "root", Path: "1", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	user := model.User{TenantID: tenant.ID, Username: f.user.Username, PasswordHash: f.user.PasswordHash, DepartmentID: dept.ID, Status: "active"}
	require.NoError(t, q.Create(&user).Error)
	return user
}

func tenantLoginRequest(t *testing.T, f *identityFixture, router *gin.Engine, code string, account, password string, ip int) (*httptest.ResponseRecorder, authEnvelope) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"tenant": code, "account": account, "password": password})
	require.NoError(t, err)
	request := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(string(body))).WithContext(f.ctx)
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", ip)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var out authEnvelope
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &out))
	return response, out
}

func TestIdentityTenantCodeLoginIntegrationSelection(t *testing.T) {
	// BE-IDN-001 §5.1/2/4/5/9/10: code, legacy ID, default and profile share one contract.
	f := newIdentityFixture(t)
	acme := addLoginTenant(t, f, "acme")
	router := identityRouter(t, f)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	identity.RegisterProfileRoutes(router, f.service, profiles)
	for _, tc := range []struct {
		name       string
		selector   map[string]any
		userID     int64
		code       string
		deprecated string
	}{
		{"default code", map[string]any{"tenant": "default"}, f.user.ID, "default", ""},
		{"acme code", map[string]any{"tenant": "acme"}, acme.ID, "acme", ""},
		{"code takes precedence", map[string]any{"tenant": "acme", "tenant_id": 1}, acme.ID, "acme", ""},
		{"legacy ID", map[string]any{"tenant_id": 1}, f.user.ID, "default", "1"},
		{"omitted", map[string]any{}, f.user.ID, "default", ""},
		{"zero ID", map[string]any{"tenant_id": 0}, f.user.ID, "default", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.selector["account"], tc.selector["password"] = f.user.Username, f.password
			response, out := authRequest(t, router, "/api/v1/auth/login", tc.selector, "")
			require.Equal(t, 200, response.Code)
			require.Equal(t, tc.deprecated, response.Header().Get("X-Tenant-Deprecated"))
			require.Equal(t, tc.userID, out.Data.User.ID)
			me, _ := profileRequest(t, router, "/api/v1/auth/me", out.Data.AccessToken)
			require.Equal(t, 200, me.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(me.Body.Bytes(), &body))
			user := body["data"].(map[string]any)["user"].(map[string]any)
			require.Equal(t, tc.code, user["tenant_code"])
			validateOpenAPIResponse(t, readIdentityOpenAPI(t), "/api/v1/auth/me", "get", me.Code, me.Body.Bytes())
			t.Logf("HTTP %d; tenant_code=%s; deprecated=%q", response.Code, tc.code, tc.deprecated)
		})
	}
}

func TestIdentityTenantCodeLoginIntegrationCredentials(t *testing.T) {
	// BE-IDN-001 §5.3/6: unknown code and bad password are identical; suspension follows bcrypt.
	f := newIdentityFixture(t)
	acme := addLoginTenant(t, f, "acme")
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE tenant SET status='suspended' WHERE id=?", acme.TenantID))
	router := identityRouter(t, f)
	wrong := accountPassword(t)
	for i, tc := range []struct {
		code, password string
		status, codeID int
	}{
		{"missing", f.password, 401, 20101},
		{"default", wrong, 401, 20101},
		{"acme", wrong, 401, 20101},
		{"acme", f.password, 403, 20111},
		{"missing", wrong, 401, 20101},
	} {
		response, out := tenantLoginRequest(t, f, router, tc.code, f.user.Username, tc.password, i+10)
		require.Equal(t, tc.status, response.Code)
		require.Equal(t, tc.codeID, out.Code)
		if tc.status == 401 {
			var body struct{ Message string }
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Equal(t, "企业代码、账号或密码错误", body.Message)
		}
		validateOpenAPIResponse(t, readIdentityOpenAPI(t), "/api/v1/auth/login", "post", response.Code, response.Body.Bytes())
	}
	var count int64
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("sys_refresh_token").Count(&count).Error)
	require.Zero(t, count)
	t.Log("unknown code / wrong password / suspended+wrong = 401/20101 with identical message; suspended+correct = 403/20111; issued sessions=0")
}

func TestIdentityTenantCodeLoginIntegrationFailureIsolation(t *testing.T) {
	// BE-IDN-001 §5.8: all selectors converge to code, and the other tenant remains usable.
	f := newIdentityFixture(t)
	acme := addLoginTenant(t, f, "acme")
	router := identityRouter(t, f)
	for i := 0; i < 5; i++ {
		response, _ := tenantLoginRequest(t, f, router, "default", f.user.Username, accountPassword(t), i+20)
		require.Contains(t, []int{401, 423}, response.Code)
	}
	value, err := f.server.Get("login:fail:default:" + f.user.Username)
	require.NoError(t, err)
	require.Equal(t, "5", value)
	require.False(t, f.server.Exists("login:fail:acme:"+f.user.Username))
	response, out := tenantLoginRequest(t, f, router, "acme", f.user.Username, f.password, 40)
	require.Equal(t, 200, response.Code)
	require.Equal(t, acme.ID, out.Data.User.ID)
	response, _ = tenantLoginRequest(t, f, router, "acme", f.user.Username, accountPassword(t), 41)
	require.Equal(t, 401, response.Code)
	response, _ = authRequest(t, router, "/api/v1/auth/login", map[string]any{"tenant_id": acme.TenantID, "account": f.user.Username, "password": f.password}, "")
	require.Equal(t, 200, response.Code)
	require.Equal(t, "1", response.Header().Get("X-Tenant-Deprecated"))
	require.False(t, f.server.Exists("login:fail:acme:"+f.user.Username))
	value, err = f.server.Get("login:fail:default:" + f.user.Username)
	require.NoError(t, err)
	require.Equal(t, "5", value)
	t.Log("default/operator failures=5; acme/operator login=200; legacy-ID success clears only acme failure key")
}

func TestIdentityTenantCodeLoginIntegrationTenantLimit(t *testing.T) {
	// BE-IDN-001 §5.7: changing the account or IP cannot evade the tenant failure window.
	f := newIdentityFixture(t)
	addLoginTenant(t, f, "acme")
	router := identityRouter(t, f)
	require.NoError(t, f.server.Set("login:rate:acme", "99"))
	f.server.SetTTL("login:rate:acme", time.Minute)
	response, _ := tenantLoginRequest(t, f, router, "acme", "missing-account", f.password, 49)
	require.Equal(t, 401, response.Code)
	failures, err := f.server.Get("login:rate:acme")
	require.NoError(t, err)
	require.Equal(t, "100", failures)
	response, out := tenantLoginRequest(t, f, router, "acme", "another-account", f.password, 50)
	require.Equal(t, 429, response.Code)
	require.Equal(t, 10005, out.Code)
	response, _ = tenantLoginRequest(t, f, router, "default", f.user.Username, f.password, 51)
	require.Equal(t, 200, response.Code)
	f.server.FastForward(time.Minute)
	response, _ = tenantLoginRequest(t, f, router, "acme", f.user.Username, f.password, 52)
	require.Equal(t, 200, response.Code)
	t.Log("acme 100 failures across accounts => next account/IP 429/10005; default unaffected; acme recovers after 1 minute")
}

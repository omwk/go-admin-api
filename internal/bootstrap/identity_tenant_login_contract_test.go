package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantCodeLoginOpenAPIContract(t *testing.T) {
	doc := readIdentityOpenAPI(t)
	schemas := doc["components"].(contractMap)["schemas"].(contractMap)
	login := schemas["LoginRequest"].(contractMap)["properties"].(contractMap)
	code := login["tenant"].(contractMap)
	require.Equal(t, "string", code["type"])
	require.EqualValues(t, 64, code["maxLength"])
	legacy := login["tenant_id"].(contractMap)
	require.Equal(t, true, legacy["deprecated"])
	require.NotContains(t, legacy, "default", "legacy IDs must not set the login default")
	user := schemas["User"].(contractMap)
	require.Contains(t, user["required"].([]any), "tenant_code")
	require.Equal(t, "string", user["properties"].(contractMap)["tenant_code"].(contractMap)["type"])
	operation := doc["paths"].(contractMap)["/api/v1/auth/login"].(contractMap)["post"].(contractMap)
	success := operation["responses"].(contractMap)["200"].(contractMap)
	require.Contains(t, success["headers"].(contractMap), "X-Tenant-Deprecated")
	t.Log("tenant string/maxLength=64; tenant_id deprecated without ID default; user.tenant_code required; deprecated header documented")
}

func TestIdentityTenantCodeLoginIntegrationDeprecatedWarning(t *testing.T) {
	// BE-IDN-001 §5.4: compatibility emits a sanitized structured WARN even on failure.
	f := newIdentityFixture(t)
	var logs bytes.Buffer
	log, err := logger.New(&logs, "warn", "json")
	require.NoError(t, err)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := logger.WithTraceID(c.Request.Context(), "tenant-login-compatibility")
		c.Request = c.Request.WithContext(logger.With(ctx, log))
		c.Next()
	})
	identity.RegisterAuthRoutes(router, f.service, "default")
	for _, password := range []string{f.password, accountPassword(t)} {
		response, _ := authRequest(t, router, "/api/v1/auth/login", map[string]any{"tenant_id": 1, "account": f.user.Username, "password": password}, "")
		require.Contains(t, []int{200, 401}, response.Code)
		require.Equal(t, "1", response.Header().Get("X-Tenant-Deprecated"))
	}
	require.Equal(t, 2, bytes.Count(logs.Bytes(), []byte("\"level\":\"WARN\"")))
	for _, field := range []string{"identity.login", "tenant-login-compatibility"} {
		require.Contains(t, logs.String(), field)
	}
	for _, sensitive := range []string{f.password, f.user.Username, "access_token", "refresh_token"} {
		require.NotContains(t, logs.String(), sensitive)
	}
	t.Log("legacy ID: success/error both carry X-Tenant-Deprecated=1; two WARN records with trace_id and no account/password/tokens")
}

func TestIdentityTenantCodeLoginIntegrationCachedProfile(t *testing.T) {
	// BE-IDN-001 §5.9: a profile cached by the previous version still returns its tenant code.
	f := newIdentityFixture(t)
	store := newProfileStore(f.database)
	profiles, err := identity.NewProfileService(store)
	require.NoError(t, err)
	cache := authz.New(f.redis.cache)
	profiles.UseAuthz(cache, store.account)
	_, err = cache.Load(f.ctx, 1, f.user.ID, "perm", func(context.Context) ([]byte, error) {
		return json.Marshal(map[string]any{
			"user":        map[string]any{"id": f.user.ID, "tenant_id": 1, "username": f.user.Username, "dept_id": f.user.DepartmentID, "is_super": false, "roles": []any{}},
			"permissions": []string{},
		})
	})
	require.NoError(t, err)
	profile, err := profiles.Me(f.ctx, 1, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, "default", profile.User.TenantCode)
	t.Log("previous cached profile without tenant_code => current response tenant_code=default")
}

func TestIdentityTenantCodeLoginIntegrationLengthAndExactMatch(t *testing.T) {
	f := newIdentityFixture(t)
	router := identityRouter(t, f)
	for i, code := range []string{"DEFAULT", " default", "default ", "unknown"} {
		response, out := tenantLoginRequest(t, f, router, code, f.user.Username, f.password, i+60)
		require.Equal(t, 401, response.Code)
		require.Equal(t, 20101, out.Code)
	}
	response, out := authRequest(t, router, "/api/v1/auth/login", map[string]any{"tenant": "unknown", "tenant_id": 1, "account": f.user.Username, "password": f.password}, "")
	require.Equal(t, 401, response.Code, "unknown explicit code must not fall back to a valid legacy ID")
	require.Equal(t, 20101, out.Code)
	require.Empty(t, response.Header().Get("X-Tenant-Deprecated"))
	oversized := string(bytes.Repeat([]byte("a"), 65))
	response, _ = tenantLoginRequest(t, f, router, oversized, f.user.Username, f.password, 70)
	require.Equal(t, 400, response.Code)
	maximum := oversized[:64]
	acme := addLoginTenant(t, f, maximum)
	response, out = tenantLoginRequest(t, f, router, maximum, f.user.Username, f.password, 71)
	require.Equal(t, 200, response.Code)
	require.Equal(t, acme.ID, out.Data.User.ID)
	f.server.FastForward(time.Minute)
	t.Log("codes match exactly; unknown explicit code overrides valid ID; length 65 rejected/64 accepted")
}

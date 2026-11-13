package bootstrap

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantIntegrationPlatformAndOrdinaryBoundaries(t *testing.T) {
	f := newTenantFixture(t)
	row := f.template(t, "boundary")
	result := f.provision(t, row.ID, "ordinary-b")
	tokens, _ := f.accept(t, result, "ordinary-b-admin")
	w, out := f.request(t, "POST", tenantPath(1)+"/disable", nil, "")
	require.Equal(t, 409, w.Code)
	require.Contains(t, string(w.Body.Bytes()), "平台租户不可停用")
	assertTenantStatus(t, f, 1, "active")
	f.bearer = tokens.AccessToken
	for _, id := range []int64{1, 999999} {
		for _, attempt := range []struct {
			method, path string
			input        any
		}{
			{"GET", tenantPath(id), nil}, {"PUT", tenantPath(id), map[string]any{"name": "forbidden"}},
			{"POST", tenantPath(id) + "/disable", nil}, {"POST", tenantPath(id) + "/enable", nil},
		} {
			w, out = f.request(t, attempt.method, attempt.path, attempt.input, "")
			require.Equal(t, 404, w.Code)
			require.Equal(t, 10003, out.Code)
		}
	}
	w, _ = f.request(t, "GET", "/admin/tenants", nil, "")
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), `"id":1,`)
	w, _ = f.request(t, "PUT", tenantPath(result.TenantID), map[string]any{"name": "own name"}, "")
	require.Equal(t, 200, w.Code)
	w, _ = f.request(t, "GET", "/admin/tenant-templates", nil, "")
	require.Equal(t, 404, w.Code)
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	f.bearer = f.login(t).AccessToken
	for _, attempt := range []struct {
		method, path string
		input        any
	}{
		{"GET", "/admin/tenants", nil}, {"POST", "/admin/tenants", tenantProvisionInput(row.ID, "denied")},
		{"POST", tenantPath(1) + "/enable", nil}, {"GET", "/admin/tenant-templates", nil},
	} {
		w, _ = f.request(t, attempt.method, attempt.path, attempt.input, "")
		require.Equal(t, 403, w.Code)
	}
	t.Log("configured platform protected by 409; ordinary foreign existing/absent read/write both404; missing permissions403")
}

func TestIdentityTenantIntegrationPlatformConfigurationMovesBoundary(t *testing.T) {
	f := newTenantFixture(t)
	row := f.template(t, "config-boundary")
	result := f.provision(t, row.ID, "platform-two")
	require.EqualValues(t, 2, result.TenantID)
	tokens, _ := f.accept(t, result, "platform-two-admin")
	path := filepath.Join(t.TempDir(), "tenant.yaml")
	require.NoError(t, os.WriteFile(path, []byte("tenant:\n  platform_id: 2\n"), 0o600))
	cfg, err := config.Load(path)
	require.NoError(t, err)
	cfg.JWT.Secret = base64.RawURLEncoding.EncodeToString(f.secret)
	f.router = gin.New()
	require.NoError(t, registerTenantRoutes(f.router, f.identityFixture.service, f.profiles, f.service, f.database, cfg))
	w, _ := f.request(t, "POST", tenantPath(result.TenantID)+"/disable", nil, "")
	require.Equal(t, 404, w.Code, "the former platform administrator loses cross-tenant authority")
	f.bearer = tokens.AccessToken
	w, _ = f.request(t, "POST", tenantPath(result.TenantID)+"/disable", nil, "")
	require.Equal(t, 409, w.Code)
	assertTenantStatus(t, f, result.TenantID, "active")
	for _, action := range []string{"disable", "enable"} {
		w, _ = f.request(t, "POST", tenantPath(1)+"/"+action, nil, "")
		require.Equal(t, 200, w.Code)
	}
	assertTenantStatus(t, f, 1, "active")
	t.Log("tenant.platform_id=2: tenant2 super manages tenant1; tenant1 super foreign404; tenant2 disable409")
}

func assertTenantStatus(t *testing.T, f *tenantFixture, id int64, status string) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row model.Tenant
	require.NoError(t, q.Where("id=?", id).Take(&row).Error)
	require.Equal(t, status, row.Status)
}

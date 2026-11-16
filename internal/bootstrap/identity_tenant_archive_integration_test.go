package bootstrap

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantArchiveIntegrationLifecycle(t *testing.T) {
	// BE-IDN-011 §4.1–3,6: archive an active tenant, deny all access, then restore.
	f := newTenantFixture(t)
	result := f.provision(t, f.template(t, "archive").ID, "archive-b")
	tokens, password := f.accept(t, result, "archive-b-admin")
	before := tenantAllCounts(t, f)
	w, out := f.request(t, "POST", tenantPath(result.TenantID)+"/archive", nil, "archive-once")
	require.Equal(t, 200, w.Code)
	var archived model.Tenant
	require.NoError(t, json.Unmarshal(out.Data, &archived))
	require.Equal(t, "suspended", archived.Status)
	require.JSONEq(t, `"`+f.now.Format("2006-01-02T15:04:05Z07:00")+`"`, tenantDeletedAt(t, out.Data))
	w, out = f.request(t, "GET", "/admin/tenants", nil, "")
	require.Equal(t, 200, w.Code)
	require.NotContains(t, string(out.Data), `"code":"archive-b"`)
	w, out = f.request(t, "GET", "/admin/tenants?include_archived=true", nil, "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, string(out.Data), `"code":"archive-b"`)
	require.Contains(t, string(out.Data), `"deleted_at":"`)
	w, _ = f.request(t, "GET", tenantPath(result.TenantID), nil, "")
	require.Equal(t, 200, w.Code)
	w, _ = f.request(t, "POST", tenantPath(result.TenantID)+"/enable", nil, "")
	require.Equal(t, 409, w.Code)
	for _, attempt := range []struct {
		path string
		body any
	}{
		{"/auth/login", map[string]any{"tenant_id": result.TenantID, "account": "archive-b-admin", "password": password}},
		{"/auth/refresh", map[string]any{"refresh_token": tokens.RefreshToken}},
	} {
		w, out = f.request(t, "POST", attempt.path, attempt.body, "")
		require.Equal(t, 403, w.Code)
		require.Equal(t, errs.ErrTenantSuspended, out.Code)
	}
	platformBearer := f.bearer
	f.bearer = tokens.AccessToken
	w, out = f.request(t, "GET", "/admin/tenants", nil, "")
	require.Equal(t, 403, w.Code)
	require.Equal(t, errs.ErrTenantSuspended, out.Code)
	f.bearer = platformBearer
	w, _ = f.request(t, "POST", tenantPath(result.TenantID)+"/restore", nil, "")
	require.Equal(t, 200, w.Code)
	w, out = f.request(t, "GET", "/admin/tenants", nil, "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, string(out.Data), `"code":"archive-b"`)
	w, _ = f.request(t, "POST", "/auth/login", map[string]any{"tenant_id": result.TenantID, "account": "archive-b-admin", "password": password}, "")
	require.Equal(t, 200, w.Code)
	after := tenantAllCounts(t, f)
	for table, count := range before {
		if table != "audit_log" && table != "idempotency_key" {
			require.Equal(t, count, after[table], table)
		}
	}
	t.Log("archive=200; default hidden/include_archived=true visible; detail=200; login/refresh/API=403/20111; restore/login=200; data preserved")
}

func tenantDeletedAt(t *testing.T, data json.RawMessage) string {
	t.Helper()
	var row map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &row))
	return string(row["deleted_at"])
}

func TestIdentityTenantArchiveIntegrationPlatformOnly(t *testing.T) {
	f := newTenantFixture(t)
	for _, action := range []string{"archive", "restore"} {
		w, _ := f.request(t, "POST", tenantPath(1)+"/"+action, nil, "")
		require.Equal(t, 409, w.Code)
	}
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	f.bearer = f.login(t).AccessToken
	for _, action := range []string{"archive", "restore"} {
		w, _ := f.request(t, "POST", tenantPath(1)+"/"+action, nil, "")
		require.Equal(t, 404, w.Code)
	}
}

func TestIdentityTenantArchiveIntegrationRepeatedArchive(t *testing.T) {
	f := newTenantFixture(t)
	result := f.provision(t, f.template(t, "archive-repeat").ID, "archive-repeat")
	path := tenantPath(result.TenantID)
	w, _ := f.request(t, "POST", path+"/restore", nil, "")
	require.Equal(t, 409, w.Code)
	w, first := f.request(t, "POST", path+"/archive", nil, "")
	require.Equal(t, 200, w.Code)
	f.now = f.now.Add(time.Minute)
	w, again := f.request(t, "POST", path+"/archive", nil, "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, tenantDeletedAt(t, first.Data), tenantDeletedAt(t, again.Data))
	w, restored := f.request(t, "POST", path+"/restore", nil, "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, "null", tenantDeletedAt(t, restored.Data))
	w, _ = f.request(t, "GET", "/admin/tenants?include_archived=invalid", nil, "")
	require.Equal(t, 400, w.Code)
	t.Log("repeat archive preserves original deleted_at; restore clears it; restore of unarchived=409; malformed include_archived=400")
}

package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantIntegrationCodeAndEncryptedIdempotency(t *testing.T) {
	f := newTenantFixture(t)
	row := f.template(t, "replay-template")
	in := tenantProvisionInput(row.ID, "replay-b")
	w, first := f.request(t, "POST", "/admin/tenants", in, "tenant-provision-replay")
	require.Equal(t, 201, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	w, second := f.request(t, "POST", "/admin/tenants", in, "tenant-provision-replay")
	require.Equal(t, 201, w.Code)
	require.JSONEq(t, string(first.Data), string(second.Data))
	var result identity.ProvisionTenantResult
	require.NoError(t, json.Unmarshal(first.Data, &result))
	w, _ = f.request(t, "POST", "/admin/tenants", in, "")
	require.Equal(t, 409, w.Code)
	in.Code = "mismatched-code"
	w, out := f.request(t, "POST", "/admin/tenants", in, "tenant-provision-replay")
	require.Equal(t, 409, w.Code)
	require.Equal(t, 10006, out.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var saved struct{ ResponseBody json.RawMessage }
	require.NoError(t, q.Table("idempotency_key").Select("response_body").Where("tenant_id=1 AND scope LIKE '%POST:/api/v1/admin/tenants'").Take(&saved).Error)
	require.False(t, bytes.Contains(saved.ResponseBody, []byte(inviteRaw(t, result.InviteURL))))
	require.False(t, bytes.Contains(saved.ResponseBody, []byte("invite_url")))
	require.Contains(t, string(saved.ResponseBody), "cipher")
	t.Log("global duplicate409; exact encrypted replay returns original invitation; changed body409/10006; raw invitation absent from idempotency storage")
}

func TestIdentityTenantIntegrationMaterializationRollsBack(t *testing.T) {
	f := newTenantFixture(t)
	row := f.template(t, "rollback-template")
	var roles []model.TemplateRole
	require.NoError(t, json.Unmarshal(row.Roles, &roles))
	roles[0].Permissions = append(roles[0].Permissions, "system:tenant:missing")
	data, err := json.Marshal(roles)
	require.NoError(t, err)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE tenant_template SET roles=?::jsonb WHERE id=?", string(data), row.ID))
	before := tenantAllCounts(t, f)
	w, _ := f.request(t, "POST", "/admin/tenants", tenantProvisionInput(row.ID, "rollback-b"), "rollback-attempt")
	require.Equal(t, 500, w.Code)
	require.Equal(t, before, tenantAllCounts(t, f))
	t.Log("missing role permission fails after tenant/menu creation; all tenant/role/menu/grant/admin/invite/audit/idempotency row counts unchanged")
}

func TestIdentityTenantIntegrationAuditFailureRollsBack(t *testing.T) {
	f := newTenantFixture(t)
	row := f.template(t, "audit-failure")
	failing, err := identity.NewAdminService(newAdminStore(f.database), func(context.Context, audit.Event) error { return errors.New("injected audit failure") })
	require.NoError(t, err)
	sealer, err := crypto.NewKeyring(map[int][]byte{1: f.secret}, 1)
	require.NoError(t, err)
	service, err := identity.NewTenantService(newTenantStore(f.database), failing, 1, "", func() time.Time { return f.now }, sealer)
	require.NoError(t, err)
	f.router = gin.New()
	require.NoError(t, identity.RegisterTenantRoutes(f.router, f.identityFixture.service, f.profiles, service))
	before := tenantAllCounts(t, f)
	w, _ := f.request(t, "POST", "/admin/tenants", tenantProvisionInput(row.ID, "audit-rollback-b"), "audit-failed-attempt")
	require.Equal(t, 500, w.Code)
	require.Equal(t, before, tenantAllCounts(t, f))
}

func TestIdentityTenantIntegrationRoleCodeCollision(t *testing.T) {
	f := newTenantFixture(t)
	row := f.template(t, "role-collision")
	var roles []model.TemplateRole
	require.NoError(t, json.Unmarshal(row.Roles, &roles))
	roles = append(roles, roles[0])
	data, err := json.Marshal(roles)
	require.NoError(t, err)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE tenant_template SET roles=?::jsonb WHERE id=?", string(data), row.ID))
	result := f.provision(t, row.ID, "collision-b")
	snap := f.snapshot(t, result.TenantID)
	require.Len(t, snap.Roles, len(roles))
	codes := map[string]bool{}
	for _, role := range snap.Roles {
		require.False(t, codes[role.Code])
		codes[role.Code] = true
	}
	require.True(t, codes[roles[0].Code+"_2"])
}

func tenantAllCounts(t *testing.T, f *tenantFixture) map[string]int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	counts := map[string]int64{}
	for _, table := range []string{"tenant", "sys_permission", "sys_menu", "sys_role", "sys_role_permission", "sys_role_menu", "sys_dept", "sys_user", "sys_user_role", "auth_token", "audit_log", "idempotency_key"} {
		var count int64
		require.NoError(t, q.Table(table).Count(&count).Error)
		counts[table] = count
	}
	return counts
}

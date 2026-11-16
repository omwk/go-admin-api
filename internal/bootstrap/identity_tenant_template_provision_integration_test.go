package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantTemplateIntegrationEmptyWarning(t *testing.T) {
	// BE-IDN-010 §4.6: clearing roles is a successful operation, including provisioning.
	f := newTenantFixture(t)
	row := blankTenantTemplate(t, f, "empty")
	putTenantTemplateRoles(t, f, row.ID, readonlyTemplateRoles())
	changed := putTenantTemplateRoles(t, f, row.ID, []model.TemplateRole{})
	assertTemplateRoles(t, changed, []model.TemplateRole{})
	w, out := f.request(t, "POST", "/admin/tenants", tenantProvisionInput(row.ID, "empty-roles"), "empty-warning")
	require.Equal(t, 201, w.Code)
	var result identity.ProvisionTenantResult
	require.NoError(t, json.Unmarshal(out.Data, &result))
	require.Equal(t, "该模板不含角色", result.Warning)
	require.Zero(t, f.count(t, "sys_role", result.TenantID))
	require.Positive(t, f.count(t, "sys_menu", result.TenantID))
	assertTenantPendingAdmin(t, f, result)
	w, replay := f.request(t, "POST", "/admin/tenants", tenantProvisionInput(row.ID, "empty-roles"), "empty-warning")
	require.Equal(t, 201, w.Code)
	require.JSONEq(t, string(out.Data), string(replay.Data))
	t.Log("6 roles=[] saved; provision HTTP201 warning=该模板不含角色; zero roles, menus/admin present; encrypted replay identical")
}

func TestIdentityTenantTemplateIntegrationCloneIndependent(t *testing.T) {
	// BE-IDN-010 §4.7: both the copy and subsequent replacements are independent.
	f := newTenantFixture(t)
	row := blankTenantTemplate(t, f, "clone-source")
	row = putTenantTemplateRoles(t, f, row.ID, readonlyTemplateRoles())
	body := map[string]any{"code": "clone-copy", "name": "只读版-2"}
	w, first := f.request(t, "POST", templatePath(row.ID)+"/clone", body, "clone-replay")
	require.Equal(t, 201, w.Code)
	var cloned model.TenantTemplate
	require.NoError(t, json.Unmarshal(first.Data, &cloned))
	require.NotEqual(t, row.ID, cloned.ID)
	require.Equal(t, "clone-copy", cloned.Code)
	require.Equal(t, "只读版-2", cloned.Name)
	require.Equal(t, row.Description, cloned.Description)
	require.JSONEq(t, string(row.Roles), string(cloned.Roles))
	require.JSONEq(t, string(row.Menus), string(cloned.Menus))
	require.JSONEq(t, string(row.Permissions), string(cloned.Permissions))
	require.JSONEq(t, string(row.Settings), string(cloned.Settings))
	w, replay := f.request(t, "POST", templatePath(row.ID)+"/clone", body, "clone-replay")
	require.Equal(t, 201, w.Code)
	require.JSONEq(t, string(first.Data), string(replay.Data))
	w, _ = f.request(t, "POST", templatePath(row.ID)+"/clone", body, "")
	require.Equal(t, 409, w.Code)
	roles := readonlyTemplateRoles()[:1]
	putTenantTemplateRoles(t, f, cloned.ID, roles)
	w, out := f.request(t, "GET", templatePath(row.ID), nil, "")
	require.Equal(t, 200, w.Code)
	var source model.TenantTemplate
	require.NoError(t, json.Unmarshal(out.Data, &source))
	assertTemplateRoles(t, source, readonlyTemplateRoles())
	putTenantTemplateRoles(t, f, row.ID, []model.TemplateRole{})
	w, out = f.request(t, "GET", templatePath(cloned.ID), nil, "")
	require.Equal(t, 200, w.Code)
	require.NoError(t, json.Unmarshal(out.Data, &cloned))
	assertTemplateRoles(t, cloned, roles)
	t.Log("7 copy has independent ID/code/roles; changes in either direction isolated; idempotent replay201 and duplicate409")
}

func TestIdentityTenantTemplateIntegrationReadonlyProvision(t *testing.T) {
	// BE-IDN-010 §4.8: real PostgreSQL materialization, with exact stored grants.
	f := newTenantFixture(t)
	row := blankTenantTemplate(t, f, "readonly-provision")
	row = putTenantTemplateRoles(t, f, row.ID, readonlyTemplateRoles())
	result := f.provision(t, row.ID, "readonly-customer")
	require.Empty(t, result.Warning)
	assertProvisionedTemplateRoles(t, f, result.TenantID, readonlyTemplateRoles())
	assertTenantPendingAdmin(t, f, result)
	t.Logf("8 readonly tenant_id=%d; operator=[trade:order:list trade:order:read], viewer=[trade:order:list]; exactly 2 roles, 3 permission grants, 4 menu grants, no write grants", result.TenantID)
}

func TestIdentityTenantTemplateIntegrationSnapshotEditable(t *testing.T) {
	// BE-IDN-010 §4.9: older snapshots retain their menu graph and gain missing catalog entries.
	f := newTenantFixture(t)
	row := f.template(t, "legacy-edit")
	roles := readonlyTemplateRoles()
	roles[0].Permissions = append(roles[0].Permissions, permission.SystemMenuList)
	// Emulate a snapshot predating a permission later added to the registry.
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE tenant_template SET permissions=(
		SELECT coalesce(jsonb_agg(item), '[]'::jsonb) FROM jsonb_array_elements(permissions) item WHERE item->>'code'<>?
	) WHERE id=?`, permission.SystemMenuList, row.ID))
	changed := putTenantTemplateRoles(t, f, row.ID, roles)
	require.JSONEq(t, string(row.Menus), string(changed.Menus))
	result := f.provision(t, row.ID, "legacy-customer")
	assertProvisionedTemplateRoles(t, f, result.TenantID, roles)
	t.Log("9 tenant snapshot edited and provisioned; missing registered catalog entries restored atomically; fixed menu graph retained")
}

func assertProvisionedTemplateRoles(t *testing.T, f *tenantFixture, tenant int64, want []model.TemplateRole) {
	t.Helper()
	snap := f.snapshot(t, tenant)
	require.Len(t, snap.Roles, len(want))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	permissionCount, menuCount := 0, 0
	for _, role := range want {
		var got model.TemplateRole
		for _, candidate := range snap.Roles {
			if candidate.Code == role.Code {
				got = candidate
			}
		}
		require.Equal(t, role.Name, got.Name)
		require.Equal(t, role.DataScope, got.DataScope)
		require.Equal(t, role.Status, got.Status)
		require.ElementsMatch(t, role.Permissions, got.Permissions)
		require.ElementsMatch(t, role.Menus, got.Menus)
		permissionCount += len(role.Permissions)
		menuCount += len(role.Menus)
	}
	require.EqualValues(t, len(want), f.count(t, "sys_role", tenant))
	require.EqualValues(t, permissionCount, f.count(t, "sys_role_permission", tenant))
	require.EqualValues(t, menuCount, f.count(t, "sys_role_menu", tenant))
	var userRoles int64
	require.NoError(t, q.Table("sys_user_role").Where("tenant_id=?", tenant).Count(&userRoles).Error)
	active := 0
	for _, role := range want {
		if role.Status == "active" {
			active++
		}
	}
	require.EqualValues(t, active, userRoles)
}

func TestIdentityTenantTemplateIntegrationBoundaries(t *testing.T) {
	// BE-IDN-010 §4.10: foreign super users and ordinary platform users both see 404.
	f := newTenantFixture(t)
	row := f.template(t, "edit-boundary")
	result := f.provision(t, row.ID, "edit-boundary-tenant")
	tokens, _ := f.accept(t, result, "edit-boundary-tenant-admin")
	platformBearer := f.bearer
	f.bearer = tokens.AccessToken
	assertTemplateOperationsHidden(t, f, row.ID)
	f.bearer = platformBearer
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	f.bearer = f.login(t).AccessToken
	assertTemplateOperationsHidden(t, f, row.ID)
	t.Log("10 template list/create/read/update/delete/clone all404/10003 for ordinary tenant super user and platform operator; existing/absent targets identical")
}

func assertTemplateOperationsHidden(t *testing.T, f *tenantFixture, id int64) {
	t.Helper()
	for _, target := range []int64{id, 999999} {
		for _, op := range []struct {
			method, path string
			body         any
		}{
			{"GET", "/admin/tenant-templates", nil},
			{"POST", "/admin/tenant-templates", map[string]any{"code": "hidden", "name": "hidden"}},
			{"GET", templatePath(target), nil},
			{"PUT", templatePath(target), map[string]any{"name": "hidden", "roles": readonlyTemplateRoles()}},
			{"DELETE", templatePath(target), nil},
			{"POST", templatePath(target) + "/clone", map[string]any{"code": "hidden", "name": "hidden"}},
		} {
			w, out := f.request(t, op.method, op.path, op.body, "")
			require.Equal(t, 404, w.Code, op.method+" "+op.path)
			require.Equal(t, 10003, out.Code)
		}
	}
}

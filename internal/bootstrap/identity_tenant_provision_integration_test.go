package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantIntegrationSnapshotProvisionAndInvite(t *testing.T) {
	// S5 §4.1–5: complete immutable framework graph, a bound invitation and tenant isolation.
	f := newTenantFixture(t)
	addTenantSnapshotEdges(t, f)
	addTenantBusinessData(t, f)
	before := canonicalTenantSnapshot(t, f.snapshot(t, 1))
	_, aMenus := profileRequest(t, f.router, "/api/v1/auth/menus", f.bearer)
	template := f.template(t, "framework-base")
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_role SET name='changed-source' WHERE tenant_id=1"))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_menu SET name='changed-source' WHERE tenant_id=1"))
	result := f.provision(t, template.ID, "tenant-b")
	require.JSONEq(t, before, canonicalTenantSnapshot(t, f.snapshot(t, result.TenantID)))
	assertTenantPendingAdmin(t, f, result)
	tokens, password := f.accept(t, result, "tenant-b-admin")
	w, bMenus := profileRequest(t, f.router, "/api/v1/auth/menus", tokens.AccessToken)
	require.Equal(t, 200, w.Code)
	require.Equal(t, canonicalTenantNavigation(aMenus.Menus), canonicalTenantNavigation(bMenus.Menus))
	require.Equal(t, aMenus.Permissions, bMenus.Permissions)
	require.Contains(t, bMenus.Permissions, permission.SystemTenantManage)
	w, _ = f.request(t, "POST", "/auth/invite/accept", map[string]any{"tenant_id": result.TenantID, "token": inviteRaw(t, result.InviteURL), "password": password}, "")
	require.Equal(t, 400, w.Code)
	require.EqualValues(t, 1, f.count(t, "sys_user", result.TenantID))
	assertTenantIsolation(t, f, result, tokens)
	var row model.Tenant
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Where("id=?", result.TenantID).Take(&row).Error)
	require.Equal(t, &template.ID, row.ProvisionedFrom)
	t.Log("snapshot preserved every role, permission, menu, parent and grant; invitation activated the reserved administrator once; login/sidebar/isolation passed")
}

func addTenantSnapshotEdges(t *testing.T, f *tenantFixture) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	group := model.Menu{TenantID: 1, Name: "unassigned-hidden", Type: "dir", Status: "active"}
	require.NoError(t, q.Create(&group).Error)
	code := permission.SystemTenantManage
	button := model.Menu{TenantID: 1, ParentID: group.ID, Name: "pathless-button", Type: "button", Status: "disabled", PermissionCode: &code}
	require.NoError(t, q.Create(&button).Error)
	path := "/snapshot-duplicate"
	for _, name := range []string{"duplicate-one", "duplicate-two"} {
		row := model.Menu{TenantID: 1, Name: name, Type: "menu", Path: &path, Status: "disabled"}
		require.NoError(t, q.Create(&row).Error)
	}
	role := model.Role{TenantID: 1, Code: "disabled-template-role", Name: "disabled-role", DataScope: "self", Status: "disabled"}
	require.NoError(t, q.Create(&role).Error)
	require.NoError(t, q.Create(&model.RoleMenu{TenantID: 1, RoleID: role.ID, MenuID: button.ID}).Error)
	var point model.Permission
	require.NoError(t, q.Where("tenant_id=1 AND code=?", code).Take(&point).Error)
	require.NoError(t, q.Create(&model.RolePermission{TenantID: 1, RoleID: role.ID, PermissionID: point.ID}).Error)
}

func addTenantBusinessData(t *testing.T, f *tenantFixture) {
	t.Helper()
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO platform(code,name,auth_type) VALUES ('tenant-fixture','tenant-fixture','api_key')"))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO shop(tenant_id,platform_id,name,external_shop_id,timezone,currency,owner_user_id,dept_id)
		SELECT 1,id,'source-shop','source-shop','UTC','USD',?,? FROM platform WHERE code='tenant-fixture'`, f.actor.User.ID, f.actor.User.DepartmentID))
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO product_spu(tenant_id,title,source) VALUES (1,'source-product','manual')"))
	require.EqualValues(t, 1, f.count(t, "shop", 1))
	require.EqualValues(t, 1, f.count(t, "product_spu", 1))
}

func assertTenantPendingAdmin(t *testing.T, f *tenantFixture, result identity.ProvisionTenantResult) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var user model.User
	require.NoError(t, q.Where("tenant_id=?", result.TenantID).Take(&user).Error)
	require.True(t, user.IsSuper)
	require.Equal(t, "disabled", user.Status)
	require.Empty(t, user.PasswordHash)
	var token model.AuthToken
	require.NoError(t, q.Where("tenant_id=? AND purpose='invite'", result.TenantID).Take(&token).Error)
	require.Equal(t, &user.ID, token.UserID)
	require.EqualValues(t, 1, token.MaxUses)
	require.NotEqual(t, inviteRaw(t, result.InviteURL), token.TokenHash)
}

func assertTenantIsolation(t *testing.T, f *tenantFixture, result identity.ProvisionTenantResult, tokens identity.Tokens) {
	t.Helper()
	for _, table := range []string{"shop", "product_spu", "sales_order"} {
		require.Zero(t, f.count(t, table, result.TenantID))
	}
	w, out := adminRequest(t, f.adminFixture, "GET", "/users", nil, "")
	require.Equal(t, 200, w.Code)
	require.NotContains(t, string(out.Data), "tenant-b-admin")
	f.bearer = tokens.AccessToken
	w, out = adminRequest(t, f.adminFixture, "GET", "/users", nil, "")
	require.Equal(t, 200, w.Code)
	require.NotContains(t, string(out.Data), `"username":"`+identity.SeedAdminUsername+`"`)
	require.NotContains(t, string(out.Data), `"username":"operator"`)
	w, _ = adminRequest(t, f.adminFixture, "GET", resourcePath("users", f.actor.User.ID), nil, "")
	require.Equal(t, 404, w.Code)
}

func TestIdentityTenantIntegrationTemplateMetadataAndProtection(t *testing.T) {
	f := newTenantFixture(t)
	row := f.template(t, "mutable-title")
	for _, path := range []string{"/admin/tenant-templates", templatePath(row.ID)} {
		w, _ := f.request(t, "GET", path, nil, "")
		require.Equal(t, 200, w.Code)
	}
	w, out := f.request(t, "PUT", templatePath(row.ID), identity.TemplateFields{Name: "renamed", Description: "reviewed"}, "")
	require.Equal(t, 200, w.Code)
	var changed model.TenantTemplate
	require.NoError(t, json.Unmarshal(out.Data, &changed))
	require.Equal(t, "renamed", changed.Name)
	require.JSONEq(t, string(row.Roles), string(changed.Roles))
	require.JSONEq(t, string(row.Menus), string(changed.Menus))
	require.Equal(t, row.Code, changed.Code)
	for _, field := range []string{"code", "roles", "menus", "permissions", "settings"} {
		w, _ = f.request(t, "PUT", templatePath(row.ID), map[string]any{"name": "forbidden", field: "changed"}, "")
		require.Equal(t, 400, w.Code)
	}
	f.provision(t, row.ID, "protected-template")
	w, _ = f.request(t, "DELETE", templatePath(row.ID), nil, "")
	require.Equal(t, 409, w.Code)
	unused := f.template(t, "unused-template")
	w, _ = f.request(t, "DELETE", templatePath(unused.ID), nil, "")
	require.Equal(t, 204, w.Code)
	w, _ = f.request(t, "GET", templatePath(unused.ID), nil, "")
	require.Equal(t, 404, w.Code)
}

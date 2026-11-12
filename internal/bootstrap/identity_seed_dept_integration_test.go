package bootstrap

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

type s1bRoleGrants struct {
	Role        model.Role
	Permissions []int64
	Menus       []int64
}

func s1bDepartmentCodes() []string {
	return []string{permission.SystemDeptCreate, permission.SystemDeptUpdate, permission.SystemDeptDelete}
}

func TestSeedIntegrationDepartmentUpgrade(t *testing.T) {
	// S1b: upgrade an existing 103-code catalog without changing users or other roles.
	f := newAdminFixture(t)
	s1bPrepareLegacySeed(t, f)
	beforeCounts := identityRowCounts(t, f.identityFixture)
	beforeGrants := s1bRoleSnapshot(t, f.identityFixture)
	beforeUsers := s1bUserGrants(t, f.identityFixture)
	beforeHash := seededAdminHash(t, f.identityFixture)
	require.EqualValues(t, 103, beforeCounts["sys_permission"])
	require.Len(t, s1bPermissionCatalog(t, f), 103)
	var firstCounts map[string]int64
	previousCounts := beforeCounts
	for run := 1; run <= 2; run++ {
		require.NoError(t, SeedIdentity(f.ctx, f.database, f.password))
		afterCounts := identityRowCounts(t, f.identityFixture)
		afterGrants := s1bRoleSnapshot(t, f.identityFixture)
		s1bAssertGrantUpgrade(t, beforeGrants, afterGrants)
		require.Equal(t, beforeUsers, s1bUserGrants(t, f.identityFixture))
		require.True(t, beforeHash == seededAdminHash(t, f.identityFixture), "seed must preserve the existing administrator password")
		require.EqualValues(t, 106, afterCounts["sys_permission"])
		require.Equal(t, beforeCounts["sys_role_permission"]+3, afterCounts["sys_role_permission"])
		require.Equal(t, beforeCounts["sys_menu"]+4, afterCounts["sys_menu"])
		require.Equal(t, beforeCounts["sys_role_menu"]+4, afterCounts["sys_role_menu"])
		if run == 1 {
			firstCounts = afterCounts
		} else {
			require.Equal(t, firstCounts, afterCounts, "second seed must not add duplicate rows")
		}
		t.Logf("seed #%d: sys_permission %d -> %d; sys_role_permission %d -> %d; sys_menu %d -> %d; sys_role_menu %d -> %d",
			run, previousCounts["sys_permission"], afterCounts["sys_permission"], previousCounts["sys_role_permission"], afterCounts["sys_role_permission"],
			previousCounts["sys_menu"], afterCounts["sys_menu"], previousCounts["sys_role_menu"], afterCounts["sys_role_menu"])
		previousCounts = afterCounts
	}
	s1bAssertHTTPProjection(t, f)
}

func s1bPrepareLegacySeed(t *testing.T, f *adminFixture) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var super model.Role
	require.NoError(t, q.Select(adminRoleColumns).Where("tenant_id = ? AND code = ?", 1, identity.SuperAdminRoleCode).Take(&super).Error)
	// An administrator-created grant outside seedRoles must also survive the upgrade.
	path := "/seed-preserved"
	extra := model.Menu{TenantID: 1, Name: "preserved", Type: "menu", Path: &path, Status: "active", Visible: true}
	require.NoError(t, q.Create(&extra).Error)
	require.NoError(t, q.Create(&model.RoleMenu{TenantID: 1, RoleID: super.ID, MenuID: extra.ID}).Error)
	var menuIDs []int64
	require.NoError(t, q.Model(&model.Menu{}).Where("tenant_id = ? AND path IN ?", 1,
		[]string{"/system/depts", "/system/depts/create", "/system/depts/update", "/system/depts/delete"}).Pluck("id", &menuIDs).Error)
	require.NoError(t, q.Where("tenant_id = ? AND menu_id IN ?", 1, menuIDs).Delete(&model.RoleMenu{}).Error)
	require.NoError(t, q.Where("tenant_id = ? AND id IN ?", 1, menuIDs).Delete(&model.Menu{}).Error)
	var permissionIDs []int64
	require.NoError(t, q.Model(&model.Permission{}).Where("tenant_id = ? AND code IN ?", 1, s1bDepartmentCodes()).Pluck("id", &permissionIDs).Error)
	require.Len(t, permissionIDs, 3)
	require.NoError(t, q.Where("tenant_id = ? AND permission_id IN ?", 1, permissionIDs).Delete(&model.RolePermission{}).Error)
	require.NoError(t, q.Where("tenant_id = ? AND id IN ?", 1, permissionIDs).Delete(&model.Permission{}).Error)
}

func s1bRoleSnapshot(t *testing.T, f *identityFixture) map[string]s1bRoleGrants {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var roles []model.Role
	require.NoError(t, q.Select(adminRoleColumns).Where("tenant_id = ?", 1).Find(&roles).Error)
	result := map[string]s1bRoleGrants{}
	for _, role := range roles {
		grants := s1bRoleGrants{Role: role}
		require.NoError(t, q.Model(&model.RolePermission{}).Where("tenant_id = ? AND role_id = ?", 1, role.ID).Order("permission_id").Pluck("permission_id", &grants.Permissions).Error)
		require.NoError(t, q.Model(&model.RoleMenu{}).Where("tenant_id = ? AND role_id = ?", 1, role.ID).Order("menu_id").Pluck("menu_id", &grants.Menus).Error)
		result[role.Code] = grants
	}
	return result
}

func s1bUserGrants(t *testing.T, f *identityFixture) []model.UserRole {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []model.UserRole
	require.NoError(t, q.Select("tenant_id, user_id, role_id").Where("tenant_id = ?", 1).Order("user_id, role_id").Find(&rows).Error)
	return rows
}

func s1bAssertGrantUpgrade(t *testing.T, before, after map[string]s1bRoleGrants) {
	t.Helper()
	require.Len(t, after, len(before))
	for code, old := range before {
		current, exists := after[code]
		require.True(t, exists)
		require.Equal(t, old.Role, current.Role, "seed must preserve existing role metadata")
		if code != identity.SuperAdminRoleCode {
			require.Equal(t, old, current, "seed changed grants of %s", code)
			continue
		}
		require.Subset(t, current.Permissions, old.Permissions)
		require.Len(t, current.Permissions, len(old.Permissions)+3)
		require.Subset(t, current.Menus, old.Menus)
		require.Len(t, current.Menus, len(old.Menus)+4)
	}
	t.Log("super_admin retains existing grants; operator/sales permissions and menus are unchanged")
}

func s1bPermissionCatalog(t *testing.T, f *adminFixture) map[string]model.Permission {
	t.Helper()
	response, envelope := adminRequest(t, f, http.MethodGet, "/permissions", nil, "")
	require.Equal(t, http.StatusOK, response.Code)
	var groups map[string][]model.Permission
	require.NoError(t, json.Unmarshal(envelope.Data, &groups))
	result := map[string]model.Permission{}
	for _, rows := range groups {
		for _, row := range rows {
			require.NotContains(t, result, row.Code, "duplicate permission code")
			result[row.Code] = row
		}
	}
	return result
}

func s1bAssertHTTPProjection(t *testing.T, f *adminFixture) {
	t.Helper()
	catalog := s1bPermissionCatalog(t, f)
	require.Len(t, catalog, 106)
	response, navigation := profileRequest(t, f.router, "/api/v1/auth/menus", f.bearer)
	require.Equal(t, http.StatusOK, response.Code)
	flattenMenuIDs(t, navigation.Menus)
	system, found := s1bNavigationNode(navigation.Menus, "/system")
	require.True(t, found)
	department, found := s1bNavigationNode(system.Children, "/system/depts")
	require.True(t, found)
	require.Equal(t, "部门", department.Name)
	require.Equal(t, "menu", department.Type)
	require.Equal(t, system.ID, department.ParentID)
	require.Equal(t, "system/dept/index", *department.Component)
	require.Equal(t, permission.SystemUserList, *department.PermissionCode)
	require.Equal(t, 4, department.Sort)
	require.Empty(t, department.Children, "navigation must omit button nodes")
	response, envelope := adminRequest(t, f, http.MethodGet, "/menus", nil, "")
	require.Equal(t, http.StatusOK, response.Code)
	var management []identity.AdminMenu
	require.NoError(t, json.Unmarshal(envelope.Data, &management))
	root, found := s1bManagementNode(management, "/system")
	require.True(t, found)
	dept, found := s1bManagementNode(root.Children, "/system/depts")
	require.True(t, found)
	require.Len(t, dept.Children, 3)
	for i, code := range s1bDepartmentCodes() {
		require.Contains(t, catalog, code)
		require.Contains(t, navigation.Permissions, code)
		button := dept.Children[i]
		require.Equal(t, "button", button.Type)
		require.Equal(t, dept.ID, button.ParentID)
		require.Equal(t, code, *button.PermissionCode)
		require.Equal(t, i+1, button.Sort)
		require.NoError(t, permission.Registered(code))
	}
	require.NotContains(t, catalog, "system:dept:list")
	t.Log("HTTP: /admin/permissions=106; /auth/menus has department navigation and three flat codes; /admin/menus has three department buttons")
}

func s1bNavigationNode(nodes []identity.MenuNode, path string) (identity.MenuNode, bool) {
	for _, node := range nodes {
		if node.Path != nil && *node.Path == path {
			return node, true
		}
	}
	return identity.MenuNode{}, false
}

func s1bManagementNode(nodes []identity.AdminMenu, path string) (identity.AdminMenu, bool) {
	for _, node := range nodes {
		if node.Path != nil && *node.Path == path {
			return node, true
		}
	}
	return identity.AdminMenu{}, false
}

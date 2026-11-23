package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestSeedGatewayMenusIntegrationVisibilityAndPermissions(t *testing.T) {
	// BE-IDN-030 §3.1/2: real /auth/menus with a super user and a restricted role.
	f := newAdminFixture(t)
	w, navigation := profileRequest(t, f.router, "/api/v1/auth/menus", f.bearer)
	require.Equal(t, 200, w.Code)
	root, found := s1bNavigationNode(navigation.Menus, "/open-platform")
	require.True(t, found)
	require.Equal(t, "开放平台", root.Name)
	require.Len(t, root.Children, 4)
	paths := []string{"/open-platform/clients", "/open-platform/routes", "/open-platform/calls", "/open-platform/usage"}
	rows := seedMenuRows(t, f.identityFixture, 1)
	require.Equal(t, 5, rows["/open-platform"].Sort)
	for i, path := range paths {
		require.Equal(t, path, *root.Children[i].Path)
		require.Equal(t, i+1, rows[path].Sort)
		require.True(t, rows[path].Visible)
	}
	raw, err := json.Marshal(root)
	require.NoError(t, err)
	t.Logf("#1 GET /api/v1/auth/menus super administrator: %s", raw)

	role := createAdminRole(t, f, "gateway-reader")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, path := range append([]string{"/open-platform"}, paths...) {
		require.NoError(t, q.Create(&model.RoleMenu{TenantID: 1, RoleID: role.ID, MenuID: rows[path].ID}).Error)
	}
	var permissions []model.Permission
	require.NoError(t, q.Select("id").Where("tenant_id=1 AND code IN ?", []string{permission.GatewayClientList, permission.GatewayCallRead}).Find(&permissions).Error)
	require.Len(t, permissions, 2)
	for _, p := range permissions {
		require.NoError(t, q.Create(&model.RolePermission{TenantID: 1, RoleID: role.ID, PermissionID: p.ID}).Error)
	}
	require.NoError(t, q.Create(&model.UserRole{TenantID: 1, UserID: f.user.ID, RoleID: role.ID}).Error)
	w, navigation = profileRequest(t, f.router, "/api/v1/auth/menus", f.login(t).AccessToken)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, navigation.Permissions, permission.GatewayRouteList)
	root, found = s1bNavigationNode(navigation.Menus, "/open-platform")
	require.True(t, found, "other gateway pages remain visible")
	require.Len(t, root.Children, 3)
	for _, path := range []string{paths[0], paths[2], paths[3]} {
		_, found = s1bNavigationNode(root.Children, path)
		require.True(t, found, path)
	}
	_, found = s1bNavigationNode(root.Children, paths[1])
	require.False(t, found, "a menu grant alone must not bypass the missing route permission")
	raw, err = json.Marshal(root)
	require.NoError(t, err)
	t.Logf("#2 role has all five menu grants but no gateway:route:list: %s", raw)
}

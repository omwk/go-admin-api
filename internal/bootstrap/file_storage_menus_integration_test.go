package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestSeedStorageMenusIntegrationVisibilityAndPermissions(t *testing.T) {
	// BE-IDN-024 §3.1/2: seed the exact frontend component keys and keep role filtering.
	f := newAdminFixture(t)
	w, navigation := profileRequest(t, f.router, "/api/v1/auth/menus", f.bearer)
	require.Equal(t, 200, w.Code)
	require.True(t, navigation.User.IsSuper)
	system, found := s1bNavigationNode(navigation.Menus, "/system")
	require.True(t, found)
	for i, page := range []struct{ path, name, component, permission string }{
		{"/system/storage", "存储配置", "/system/storage/index.vue", permission.SystemStorageManage},
		{"/system/files", "文件管理", "/system/files/index.vue", permission.SystemFileManage},
	} {
		node, found := s1bNavigationNode(system.Children, page.path)
		require.True(t, found, page.path)
		require.Equal(t, page.name, node.Name)
		require.Equal(t, page.component, *node.Component)
		require.Equal(t, page.permission, *node.PermissionCode)
		require.Equal(t, 12+i, node.Sort)
		require.True(t, node.Visible)
	}
	raw, err := json.Marshal(system)
	require.NoError(t, err)
	t.Logf("BE-IDN-024 superadmin /auth/menus system JSON: %s", raw)
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	w, navigation = profileRequest(t, f.router, "/api/v1/auth/menus", f.login(t).AccessToken)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, navigation.Permissions, permission.SystemStorageManage)
	raw, err = json.Marshal(navigation.Menus)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "/system/storage")
	t.Log("BE-IDN-024 account without system:storage:manage: storage menu absent")
}

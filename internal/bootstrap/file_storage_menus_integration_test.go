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
		{"/system/storage", "存储配置", "system/storage/index", permission.SystemStorageManage},
		{"/system/files", "文件管理", "system/files/index", permission.SystemFileManage},
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

func TestSeedMenuComponentIntegrationRerun(t *testing.T) {
	// BE-IDN-026 §4.2: upgrade existing rows through the real seed and HTTP projection.
	f := newAdminFixture(t)
	before := seedMenuRows(t, f.identityFixture, 1)
	components := map[string]string{"/system/storage": "system/storage/index", "/system/files": "system/files/index"}
	for path, component := range components {
		require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_menu SET component=? WHERE tenant_id=1 AND path=?", "/"+component+".vue", path))
	}
	w, legacy := profileRequest(t, f.router, "/api/v1/auth/menus", f.bearer)
	require.Equal(t, 200, w.Code)
	system, found := s1bNavigationNode(legacy.Menus, "/system")
	require.True(t, found)
	for path, component := range components {
		node, found := s1bNavigationNode(system.Children, path)
		require.True(t, found)
		require.Equal(t, "/"+component+".vue", *node.Component)
	}
	for run := 1; run <= 2; run++ {
		require.NoError(t, SeedIdentity(f.ctx, f.database, f.password))
		after := seedMenuRows(t, f.identityFixture, 1)
		require.Len(t, after, len(before))
		for path, previous := range before {
			if component, updated := components[path]; updated {
				previous.Component = &component
			}
			require.Equal(t, previous, after[path], "retain ID, path, permission and other fields for %s", path)
		}
		w, navigation := profileRequest(t, f.router, "/api/v1/auth/menus", f.bearer)
		require.Equal(t, 200, w.Code)
		assertShorthandNavigation(t, navigation.Menus)
		t.Logf("BE-IDN-026 seed run=%d: %d existing menu IDs retained; /auth/menus components are shorthand", run, len(after))
	}
}

func assertShorthandNavigation(t *testing.T, nodes []identity.MenuNode) {
	t.Helper()
	for _, node := range nodes {
		if node.Component != nil {
			require.Regexp(t, `^[a-z0-9/-]+$`, *node.Component)
			require.NotContains(t, *node.Component, ".vue")
			require.NotRegexp(t, `^/`, *node.Component)
		}
		assertShorthandNavigation(t, node.Children)
	}
}

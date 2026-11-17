package bootstrap

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestSeedMenuIntegrationUpgradeAndIdempotency(t *testing.T) {
	// BE-IDN-012 §3.2/6: upgrade the old catalog without replacing existing IDs.
	f := newAdminFixture(t)
	removeNewSeedMenus(t, f)
	before := seedMenuRows(t, f.identityFixture, 1)
	require.Len(t, before, 21)
	crm := before["/crm"]
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE sys_menu SET name='old', type='menu',
		component='old', redirect='/old', icon='old', sort=99, visible=false, keep_alive=true,
		permission_code=?, parent_id=? WHERE tenant_id=1 AND id=?`, permission.SystemUserList, before["/system"].ID, crm.ID))
	require.NoError(t, SeedIdentity(f.ctx, f.database, f.password))
	after := seedMenuRows(t, f.identityFixture, 1)
	require.Len(t, after, 28)
	require.Equal(t, crm.ID, after["/crm"].ID)
	crm.Sort = 4
	require.Equal(t, crm, after["/crm"], "all code-owned fields, including nil/false, must be restored")
	for path, old := range before {
		require.Equal(t, old.ID, after[path].ID, "seed must retain IDs and grants for %s", path)
	}
	assertSeedMenuGraph(t, after)
	require.NoError(t, SeedIdentity(f.ctx, f.database, f.password))
	require.Equal(t, after, seedMenuRows(t, f.identityFixture, 1))
	t.Log("menus 21 -> 28 -> 28; stable IDs, updated type/parent/fields, identical second seed")
}

func TestSeedMenuIntegrationPreservesCustomAndForeignMenus(t *testing.T) {
	// BE-IDN-012 §3.7: untouched rows include custom menus, metadata and other tenants.
	f := newAdminFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	path, foreignPath := "/custom-report", "/catalog/products"
	custom := model.Menu{TenantID: 1, Name: "custom-report", Type: "menu", Path: &path, Status: "disabled"}
	foreign := model.Menu{TenantID: 2, Name: "foreign-product", Type: "link", Path: &foreignPath, Status: "active", Visible: true}
	require.NoError(t, q.Create(&custom).Error)
	require.NoError(t, q.Create(&foreign).Error)
	require.NoError(t, SeedIdentity(f.ctx, f.database, f.password))
	require.Equal(t, custom, seedMenuRows(t, f.identityFixture, 1)[path])
	require.Equal(t, foreign, seedMenuRows(t, f.identityFixture, 2)[foreignPath])
	t.Log("custom menu and foreign tenant with identical seed path preserved field for field")
}

func TestSeedMenuIntegrationNewTenantNavigationAndPermissions(t *testing.T) {
	// BE-IDN-012 §3.1/3/4/5/8/10: real template provisioning and unchanged HTTP contract.
	f := newTenantFixture(t)
	template := f.template(t, "menu-seed")
	result := f.provision(t, template.ID, "menu-seed-customer")
	tokens, _ := f.accept(t, result, "menu-seed-customer-admin")
	response, navigation := profileRequest(t, f.router, "/api/v1/auth/menus", tokens.AccessToken)
	require.Equal(t, 200, response.Code)
	validateOpenAPIResponse(t, f.contract, "/api/v1/auth/menus", "get", 200, response.Body.Bytes())
	assertSeedMenuGraph(t, seedMenuRows(t, f.identityFixture, result.TenantID))
	paths := []string{}
	for _, root := range navigation.Menus {
		paths = append(paths, *root.Path)
	}
	require.Equal(t, []string{"/system", "/trade", "/catalog", "/crm"}, paths)
	system, found := s1bNavigationNode(navigation.Menus, "/system")
	require.True(t, found)
	for _, path := range []string{"/system/dicts", "/system/settings", "/system/tenants"} {
		_, found := s1bNavigationNode(system.Children, path)
		require.True(t, found, path)
	}
	catalog, found := s1bNavigationNode(navigation.Menus, "/catalog")
	require.True(t, found)
	require.Len(t, catalog.Children, 1)
	require.Equal(t, "/catalog/products", *catalog.Children[0].Path)
	require.Empty(t, catalog.Children[0].Children, "hidden detail must be absent from sidebar")
	tenants, found := s1bNavigationNode(system.Children, "/system/tenants")
	require.True(t, found)
	require.Empty(t, tenants.Children, "hidden template routes must be absent from sidebar")
	assertSeedMenuPermissionFilter(t, f)
	t.Logf("new tenant=%d: 28 menus; root order 1/2/3/4; hidden routes stored, omitted from sidebar; OpenAPI valid", result.TenantID)
}

func assertSeedMenuPermissionFilter(t *testing.T, f *tenantFixture) {
	t.Helper()
	// The built-in operator has order grants and no product permission or menu grant.
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	response, menus := profileRequest(t, f.router, "/api/v1/auth/menus", f.login(t).AccessToken)
	require.Equal(t, 200, response.Code)
	require.NotContains(t, menus.Permissions, permission.ProductSPUList)
	_, found := s1bNavigationNode(menus.Menus, "/catalog")
	if found {
		catalog, _ := s1bNavigationNode(menus.Menus, "/catalog")
		_, found = s1bNavigationNode(catalog.Children, "/catalog/products")
	}
	require.False(t, found, "unprivileged user must not receive product page")
}

func removeNewSeedMenus(t *testing.T, f *adminFixture) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	paths := []string{"/catalog", "/catalog/products", "/catalog/products/:id", "/system/dicts", "/system/settings",
		"/system/tenants/templates", "/system/tenants/templates/:id"}
	var ids []int64
	require.NoError(t, q.Model(&model.Menu{}).Where("tenant_id=1 AND path IN ?", paths).Pluck("id", &ids).Error)
	if len(ids) > 0 {
		require.NoError(t, q.Where("tenant_id=1 AND menu_id IN ?", ids).Delete(&model.RoleMenu{}).Error)
		require.NoError(t, q.Where("tenant_id=1 AND id IN ?", ids).Delete(&model.Menu{}).Error)
	}
}

func seedMenuRows(t *testing.T, f *identityFixture, tenant int64) map[string]model.Menu {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []model.Menu
	require.NoError(t, q.Select(adminMenuColumns).Where("tenant_id=?", tenant).Order("id").Find(&rows).Error)
	result := make(map[string]model.Menu, len(rows))
	for _, row := range rows {
		require.NotNil(t, row.Path)
		require.NotContains(t, result, *row.Path, "duplicate path")
		result[*row.Path] = row
	}
	return result
}

func assertSeedMenuGraph(t *testing.T, rows map[string]model.Menu) {
	t.Helper()
	for i, path := range []string{"/system", "/trade", "/catalog", "/crm"} {
		require.Equal(t, "dir", rows[path].Type)
		require.Equal(t, i+1, rows[path].Sort)
	}
	for _, pair := range [][2]string{{"/system/dicts", "/system"}, {"/system/settings", "/system"},
		{"/catalog/products", "/catalog"}, {"/catalog/products/:id", "/catalog/products"},
		{"/system/tenants/templates", "/system/tenants"}, {"/system/tenants/templates/:id", "/system/tenants"}} {
		require.Contains(t, rows, pair[0])
		require.Equal(t, rows[pair[1]].ID, rows[pair[0]].ParentID)
	}
	for _, path := range []string{"/catalog/products/:id", "/system/tenants/templates", "/system/tenants/templates/:id"} {
		require.False(t, rows[path].Visible, path)
	}
}

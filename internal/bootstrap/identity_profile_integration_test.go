package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func seededProfileFixture(t *testing.T) (*identityFixture, *gin.Engine) {
	t.Helper()
	f := newIdentityFixture(t)
	require.NoError(t, SeedIdentity(f.ctx, f.database, f.password))
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	router := identityRouter(t, f)
	identity.RegisterProfileRoutes(router, f.service, profiles)
	return f, router
}

func profileRequest(t *testing.T, router http.Handler, path, bearer string) (*httptest.ResponseRecorder, identity.MenusResponse) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var envelope struct {
		Data identity.MenusResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	return response, envelope.Data
}

func TestIdentityProfileIntegrationSeedAndAggregatedReads(t *testing.T) {
	// REQ-IAM-006/011: both reads return the same safe current identity and flat permissions.
	f, router := seededProfileFixture(t)
	tokens, err := f.service.Login(f.ctx, 1, identity.SeedAdminUsername, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	response, menus := profileRequest(t, router, "/api/v1/auth/menus", tokens.AccessToken)
	require.Equal(t, 200, response.Code)
	require.NotContains(t, response.Body.String(), "password_hash")
	require.NotContains(t, response.Body.String(), "PasswordHash")
	require.True(t, menus.User.IsSuper)
	require.NotEmpty(t, menus.Menus)
	response, me := profileRequest(t, router, "/api/v1/auth/me", tokens.AccessToken)
	require.Equal(t, 200, response.Code)
	require.Equal(t, menus.User, me.User)
	require.Equal(t, menus.Permissions, me.Permissions)
	require.Len(t, me.Permissions, len(permission.All()))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Where("tenant_id = ?", 1).Delete(&model.RolePermission{}).Error)
	_, superWithoutGrants := profileRequest(t, router, "/api/v1/auth/me", tokens.AccessToken)
	require.Equal(t, me.Permissions, superWithoutGrants.Permissions, "is_super reads the entire permission catalog even without role grants")
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/auth/menus"} {
		response, _ = profileRequest(t, router, path, "")
		require.Equal(t, 401, response.Code)
		require.Contains(t, response.Body.String(), `"code":20103`)
	}
}

func TestIdentityProfileIntegrationRolesAndHiddenAncestors(t *testing.T) {
	f, router := seededProfileFixture(t)
	assignProfileRole(t, f, identity.OperatorRoleCode)
	operator := f.login(t)
	_, operatorMenus := profileRequest(t, router, "/api/v1/auth/menus", operator.AccessToken)
	operatorIDs := flattenMenuIDs(t, operatorMenus.Menus)
	require.NotEmpty(t, operatorIDs)
	assignProfileRole(t, f, identity.SalesRoleCode)
	sales := f.login(t)
	_, salesMenus := profileRequest(t, router, "/api/v1/auth/menus", sales.AccessToken)
	require.NotEqual(t, operatorIDs, flattenMenuIDs(t, salesMenus.Menus))
	require.NotEqual(t, operatorMenus.Permissions, salesMenus.Permissions)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Model(&model.Menu{}).Where("tenant_id = ? AND path = ?", 1, "/crm/customers").Update("visible", false).Error)
	_, hiddenLeaf := profileRequest(t, router, "/api/v1/auth/menus", sales.AccessToken)
	require.Len(t, hiddenLeaf.Menus, 1)
	require.Len(t, hiddenLeaf.Menus[0].Children, 1)
	require.Equal(t, "/crm/inquiries", *hiddenLeaf.Menus[0].Children[0].Path)
	require.NoError(t, q.Model(&model.Menu{}).Where("tenant_id = ? AND path = ?", 1, "/crm").Update("visible", false).Error)
	_, hidden := profileRequest(t, router, "/api/v1/auth/menus", sales.AccessToken)
	require.Empty(t, hidden.Menus)
	admin, err := f.service.Login(f.ctx, 1, identity.SeedAdminUsername, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	_, adminMenus := profileRequest(t, router, "/api/v1/auth/menus", admin.AccessToken)
	for _, node := range adminMenus.Menus {
		if node.Path != nil {
			require.NotEqual(t, "/crm", *node.Path)
		}
	}
}

func TestIdentityProfileIntegrationDisabledUserIsDenied(t *testing.T) {
	f, router := seededProfileFixture(t)
	issued := f.login(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Model(&model.User{}).Where("tenant_id = ? AND id = ?", 1, f.user.ID).Update("status", "disabled").Error)
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/auth/menus"} {
		response, _ := profileRequest(t, router, path, issued.AccessToken)
		require.Equal(t, 401, response.Code)
	}
}

func TestIdentitySeedIntegrationIsIdempotentAndPreservesPassword(t *testing.T) {
	f, _ := seededProfileFixture(t)
	firstCounts := identityRowCounts(t, f)
	before := seededAdminHash(t, f)
	replacement, err := refresh.Generate()
	require.NoError(t, err)
	require.NoError(t, SeedIdentity(f.ctx, f.database, replacement))
	require.Equal(t, firstCounts, identityRowCounts(t, f))
	after := seededAdminHash(t, f)
	require.Equal(t, before, after)
	_, err = f.service.Login(f.ctx, 1, identity.SeedAdminUsername, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	_, err = f.service.Login(f.ctx, 1, identity.SeedAdminUsername, replacement, identity.RequestMeta{})
	require.Error(t, err)
}

func seededAdminHash(t *testing.T, f *identityFixture) string {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var user model.User
	require.NoError(t, q.Select("password_hash").Where("tenant_id = ? AND username = ?", 1, identity.SeedAdminUsername).Take(&user).Error)
	return user.PasswordHash
}

func identityRowCounts(t *testing.T, f *identityFixture) map[string]int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	counts := map[string]int64{}
	for _, table := range []string{"sys_permission", "sys_menu", "sys_role", "sys_role_menu", "sys_role_permission", "sys_user", "sys_dept", "sys_user_role"} {
		var count int64
		require.NoError(t, q.Table(table).Where("tenant_id = ?", 1).Count(&count).Error)
		counts[table] = count
	}
	return counts
}

func assignProfileRole(t *testing.T, f *identityFixture, code string) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var role model.Role
	require.NoError(t, q.Select("id").Where("tenant_id = ? AND code = ?", 1, code).Take(&role).Error)
	require.NoError(t, q.Where("tenant_id = ? AND user_id = ?", 1, f.user.ID).Delete(&model.UserRole{}).Error)
	require.NoError(t, q.Create(&model.UserRole{TenantID: 1, UserID: f.user.ID, RoleID: role.ID}).Error)
}

func flattenMenuIDs(t *testing.T, nodes []identity.MenuNode) []int64 {
	t.Helper()
	ids := []int64{}
	for _, node := range nodes {
		require.NotEqual(t, "button", node.Type)
		ids = append(ids, node.ID)
		ids = append(ids, flattenMenuIDs(t, node.Children)...)
	}
	return ids
}

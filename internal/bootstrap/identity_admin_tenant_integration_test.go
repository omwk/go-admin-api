package bootstrap

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

func TestAdminIntegrationMissingCrossTenantAndZeroRows(t *testing.T) {
	f := newAdminFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	dept := model.Department{TenantID: 2, Name: "foreign", Path: "foreign", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	user := model.User{TenantID: 2, Username: "foreign", PasswordHash: f.user.PasswordHash, DepartmentID: dept.ID, Status: "active"}
	require.NoError(t, q.Create(&user).Error)
	role := model.Role{TenantID: 2, Code: "foreign", Name: "foreign", DataScope: "all", Status: "active"}
	require.NoError(t, q.Create(&role).Error)
	menu := model.Menu{TenantID: 2, Name: "foreign", Type: "menu", Status: "active"}
	require.NoError(t, q.Create(&menu).Error)
	for _, tc := range []struct {
		resource string
		id       int64
		body     any
	}{
		{"users", user.ID, identity.UserFields{DepartmentID: f.user.DepartmentID, Status: "active"}},
		{"roles", role.ID, identity.RoleRequest{Code: "x", Name: "x", DataScope: "self", Status: "active"}},
		{"menus", menu.ID, identity.MenuRequest{Name: "x", Type: "menu", Status: "active"}},
	} {
		for _, id := range []int64{tc.id, 9999999} {
			response, _ := adminRequest(t, f, "PUT", resourcePath(tc.resource, id), tc.body, "")
			require.Equal(t, 404, response.Code, response.Body.String())
			response, _ = adminRequest(t, f, "DELETE", resourcePath(tc.resource, id), nil, "")
			require.Equal(t, 404, response.Code)
		}
	}
	for _, err := range []error{
		f.store.UpdateUser(f.ctx, 1, user.ID, map[string]any{"status": "disabled"}), f.store.UpdateRole(f.ctx, 1, role.ID, map[string]any{"name": "changed"}), f.store.UpdateMenu(f.ctx, 1, menu.ID, map[string]any{"name": "changed"}),
		f.store.DeleteUser(f.ctx, 1, user.ID), f.store.DeleteRole(f.ctx, 1, role.ID), f.store.DeleteMenu(f.ctx, 1, menu.ID),
	} {
		require.Equal(t, 404, errs.From(err).HTTPStatus)
	}
	local := createAdminMenu(t, f, "local", 0)
	response, _ := adminRequest(t, f, "PUT", resourcePath("menus", local.ID), identity.MenuRequest{ParentID: menu.ID, Name: "local", Type: "menu", Status: "active"}, "")
	require.Equal(t, 404, response.Code)
	response, _ = adminRequest(t, f, "PUT", resourcePath("users", f.user.ID)+"/roles", map[string]any{"role_ids": []int64{role.ID}}, "")
	require.Equal(t, 404, response.Code)
	localRole := createAdminRole(t, f, "local")
	response, _ = adminRequest(t, f, "PUT", resourcePath("roles", localRole.ID)+"/depts", map[string]any{"dept_ids": []int64{dept.ID}}, "")
	require.Equal(t, 404, response.Code)
	// Composite foreign keys remain the final defense against dangling/cross-tenant links.
	require.Error(t, q.Create(&model.UserRole{TenantID: 1, UserID: f.user.ID, RoleID: role.ID}).Error)
	require.Error(t, q.Create(&model.RoleMenu{TenantID: 1, RoleID: localRole.ID, MenuID: menu.ID}).Error)
	seed := &seedStore{authStore: f.identityFixture.store}
	require.Error(t, seed.SeedPermissions(f.ctx, 1, []model.Permission{{Code: "system:user", Module: "system", Type: "api"}}))
}

func TestAdminIntegrationGrantCannotEscalateOperator(t *testing.T) {
	f := newAdminFixture(t)
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var operator model.Role
	require.NoError(t, q.Select("id").Where("tenant_id = ? AND code = ?", 1, identity.OperatorRoleCode).Take(&operator).Error)
	for _, code := range []string{permission.SystemUserGrant, permission.SystemRoleGrant, permission.SystemRoleCreate} {
		var point model.Permission
		require.NoError(t, q.Select("id").Where("code = ?", code).Take(&point).Error)
		require.NoError(t, q.Create(&model.RolePermission{TenantID: 1, RoleID: operator.ID, PermissionID: point.ID}).Error)
	}
	role := createAdminRole(t, f, "unprivileged")
	issued := f.login(t)
	f.bearer = issued.AccessToken
	response, _ := adminRequest(t, f, "POST", "/roles", identity.RoleRequest{Code: "escalated", Name: "escalated", DataScope: "all", Status: "active"}, "")
	require.Equal(t, 403, response.Code)
	response, _ = adminRequest(t, f, "PUT", resourcePath("roles", role.ID)+"/depts", map[string]any{"dept_ids": []int64{f.actor.User.DepartmentID}}, "")
	require.Equal(t, 403, response.Code)
	var point model.Permission
	require.NoError(t, q.Select("id").Where("code = ?", permission.SystemUserDelete).Take(&point).Error)
	response, _ = adminRequest(t, f, "PUT", resourcePath("roles", role.ID)+"/permissions", map[string]any{"permission_ids": []int64{point.ID}}, "")
	require.Equal(t, 403, response.Code)
	ids, err := f.store.Links(f.ctx, 1, repo.RolePermissions, role.ID)
	require.NoError(t, err)
	require.Empty(t, ids)
}

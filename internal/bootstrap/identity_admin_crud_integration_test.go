package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

func TestAdminIntegrationUserCRUDAndDedicatedRoles(t *testing.T) {
	// REQ-IAM-003/004: scalar updates and authorization endpoints have disjoint inputs.
	f := newAdminFixture(t)
	password, err := refresh.Generate()
	require.NoError(t, err)
	request := identity.CreateUserRequest{Username: "managed", Password: password, UserFields: identity.UserFields{DepartmentID: f.user.DepartmentID, Status: "active"}}
	response, envelope := adminRequest(t, f, "POST", "/users", request, "create-user")
	require.Equal(t, 201, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), password)
	require.NotContains(t, response.Body.String(), "password_hash")
	var user identity.AdminUser
	require.NoError(t, json.Unmarshal(envelope.Data, &user))
	replayed, again := adminRequest(t, f, "POST", "/users", request, "create-user")
	require.Equal(t, 201, replayed.Code)
	require.JSONEq(t, string(envelope.Data), string(again.Data))
	request.Username = "other"
	response, envelope = adminRequest(t, f, "POST", "/users", request, "create-user")
	require.Equal(t, 409, response.Code)
	require.Equal(t, 10006, envelope.Code)
	role := createAdminRole(t, f, "managed")
	path := resourcePath("users", user.ID)
	response, _ = adminRequest(t, f, "PUT", path+"/roles", map[string]any{"role_ids": []int64{role.ID}}, "")
	require.Equal(t, 204, response.Code, response.Body.String())
	request.UserFields.Status = "disabled"
	response, _ = adminRequest(t, f, "PUT", path, request.UserFields, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	ids, err := f.store.Links(f.ctx, 1, repo.UserRoles, user.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{role.ID}, ids)
	_, err = f.identityFixture.service.Authenticate(f.ctx, f.bearer)
	require.NoError(t, err)
	_, err = f.identityFixture.service.Login(f.ctx, 1, "managed", password, identity.RequestMeta{})
	require.Error(t, err)
	response, _ = adminRequest(t, f, "PUT", path, map[string]any{"dept_id": f.user.DepartmentID, "status": "active", "role_ids": []int64{}}, "")
	require.Equal(t, 400, response.Code)
	response, _ = adminRequest(t, f, "PUT", path, identity.UserFields{DepartmentID: f.user.DepartmentID, Status: "active"}, "")
	require.Equal(t, 200, response.Code)
	replacement, err := refresh.Generate()
	require.NoError(t, err)
	response, _ = adminRequest(t, f, "POST", path+"/reset-password", map[string]string{"password": replacement}, "")
	require.Equal(t, 204, response.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var stored model.User
	require.NoError(t, q.Select("password_hash").Where("id = ?", user.ID).Take(&stored).Error)
	require.NoError(t, pwd.Verify(stored.PasswordHash, replacement))
	response, _ = adminRequest(t, f, "DELETE", path, nil, "")
	require.Equal(t, 204, response.Code, response.Body.String())
	var deleted model.User
	require.NoError(t, q.Unscoped().Select("deleted_at").Where("id = ?", user.ID).Take(&deleted).Error)
	require.True(t, deleted.DeletedAt.Valid)
	response, _ = adminRequest(t, f, "DELETE", path, nil, "")
	require.Equal(t, 404, response.Code)
	response, _ = adminRequest(t, f, "GET", "/users?page=1&page_size=200", nil, "")
	require.Equal(t, 200, response.Code)
	response, _ = adminRequest(t, f, "GET", "/users?sort=invalid", nil, "")
	require.Equal(t, 400, response.Code)
}

func TestAdminIntegrationRoleGrantsAndUpdate(t *testing.T) {
	f := newAdminFixture(t)
	role := createAdminRole(t, f, "grants")
	menu := createAdminMenu(t, f, "granted", 0)
	permissions, err := f.store.Permissions(f.ctx, 1)
	require.NoError(t, err)
	var permissionID int64
	for _, p := range permissions {
		if p.Code == permission.SystemUserList {
			permissionID = p.ID
		}
	}
	path := resourcePath("roles", role.ID)
	for _, tc := range []struct {
		suffix, key string
		ids         []int64
	}{
		{"menus", "menu_ids", []int64{menu.ID}}, {"permissions", "permission_ids", []int64{permissionID}}, {"depts", "dept_ids", []int64{f.user.DepartmentID}},
	} {
		response, _ := adminRequest(t, f, "PUT", path+"/"+tc.suffix, map[string]any{tc.key: tc.ids}, "")
		require.Equal(t, 204, response.Code, response.Body.String())
		response, _ = adminRequest(t, f, "PUT", path+"/"+tc.suffix, map[string]any{tc.key: []int64{9999999}}, "")
		require.Equal(t, 404, response.Code)
	}
	response, _ := adminRequest(t, f, "PUT", path, identity.RoleRequest{Code: role.Code, Name: "renamed", DataScope: "self", Status: "disabled"}, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	for _, path := range []string{"/roles", "/permissions", "/menus"} {
		response, _ := adminRequest(t, f, "GET", path, nil, "")
		require.Equal(t, 200, response.Code, response.Body.String())
	}
}

func TestAdminIntegrationPermissionDeniedAndDisabledAccount(t *testing.T) {
	f := newAdminFixture(t)
	adminBearer := f.bearer
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	issued := f.login(t)
	f.bearer = issued.AccessToken
	response, envelope := adminRequest(t, f, "GET", "/users", nil, "")
	require.Equal(t, 403, response.Code)
	require.Equal(t, 20107, envelope.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var role model.Role
	var point model.Permission
	require.NoError(t, q.Select("id").Where("code = ?", identity.OperatorRoleCode).Take(&role).Error)
	require.NoError(t, q.Select("id").Where("code = ?", permission.SystemUserList).Take(&point).Error)
	require.NoError(t, q.Create(&model.RolePermission{TenantID: 1, RoleID: role.ID, PermissionID: point.ID}).Error)
	response, _ = adminRequest(t, f, "GET", "/users", nil, "")
	require.Equal(t, 200, response.Code)
	f.bearer = adminBearer
	response, _ = adminRequest(t, f, "PUT", resourcePath("users", f.user.ID), identity.UserFields{DepartmentID: f.user.DepartmentID, Status: "disabled"}, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	f.bearer = issued.AccessToken
	response, _ = adminRequest(t, f, "GET", "/users", nil, "")
	require.Equal(t, 401, response.Code)
	_, err := f.identityFixture.service.Login(f.ctx, 1, f.user.Username, f.password, identity.RequestMeta{})
	require.Error(t, err)
}

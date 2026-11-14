package bootstrap

import (
	"net/http"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	identitymodel "github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/stretchr/testify/require"
)

func TestCredentialIntegrationT24ScopeInheritanceAndSoftDeletion(t *testing.T) {
	// REQ-SHP-004/007: parent ownership and department protect every credential entry.
	f := newCredentialFixture(t)
	created, _ := f.create(t, "oauth2")
	q, cancel := f.database.WithContext(f.ctx)
	require.NoError(t, q.Model(&identitymodel.User{}).Where("id=?", f.actor.UserID).Update("is_super", false).Error)
	otherDept := identitymodel.Department{TenantID: 1, Name: "unrelated", Path: "999", Status: "active"}
	require.NoError(t, q.Create(&otherDept).Error)
	other := identitymodel.User{TenantID: 1, Username: "other-owner", PasswordHash: f.user.PasswordHash, DepartmentID: otherDept.ID, Status: "active"}
	require.NoError(t, q.Create(&other).Error)
	var role identitymodel.Role
	require.NoError(t, q.Select("id").Where("code = ?", identity.SuperAdminRoleCode).Take(&role).Error)
	custom := identitymodel.RoleDepartment{TenantID: 1, RoleID: role.ID, DeptID: f.actor.DepartmentID}
	require.NoError(t, q.Create(&custom).Error)
	cancel()
	for _, kind := range []string{"all", "self", "dept", "dept_and_child", "custom"} {
		t.Run(kind, func(t *testing.T) {
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			require.NoError(t, q.Model(&identitymodel.Role{}).Where("id = ?", role.ID).Update("data_scope", kind).Error)
			require.NoError(t, q.Model(&shopmodel.Shop{}).Where("id = ?", f.shop.ID).Updates(map[string]any{"owner_user_id": f.actor.UserID, "dept_id": f.actor.DepartmentID}).Error)
			_, err := f.service.GetCredential(f.ctx, f.actor, created.ID)
			require.NoError(t, err)
			response, _ := f.request(t, "GET", f.shopPath()+"/credentials", nil)
			require.Equal(t, 200, response.Code)
			// Keep the operator's own department unchanged while moving the shop.
			require.NoError(t, q.Model(&shopmodel.Shop{}).Where("id = ?", f.shop.ID).Updates(map[string]any{"owner_user_id": other.ID, "dept_id": otherDept.ID}).Error)
			if kind == "all" {
				_, err := f.service.GetCredential(f.ctx, f.actor, created.ID)
				require.NoError(t, err)
				return
			}
			f.assertInvisibleCredential(t, created.ID)
		})
	}
	q, cancel = f.database.WithContext(f.ctx)
	require.NoError(t, q.Model(&identitymodel.Role{}).Where("id = ?", role.ID).Update("data_scope", "all").Error)
	require.NoError(t, q.Where("id = ?", f.shop.ID).Delete(&shopmodel.Shop{}).Error)
	cancel()
	f.assertInvisibleCredential(t, created.ID)
}

func (f *credentialFixture) assertInvisibleCredential(t *testing.T, id int64) {
	t.Helper()
	before := f.databaseSnapshot(t)
	for _, request := range []struct {
		method, path string
		body         any
	}{
		{"GET", f.shopPath() + "/credentials", nil},
		{"POST", f.shopPath() + "/credentials", map[string]any{"kind": "oauth2", "values": map[string]string{"private_value": f.markers[0]}}},
		{"PUT", credentialPath(id), map[string]any{"values": map[string]string{"private_value": f.markers[0]}}},
		{"POST", credentialPath(id) + "/disable", nil},
		{"POST", credentialPath(id) + "/check", nil},
		{"GET", credentialPath(id) + "/audits", nil},
	} {
		response, envelope := f.request(t, request.method, request.path, request.body)
		require.Equal(t, 404, response.Code, request.path)
		require.Equal(t, 30101, envelope.Code)
	}
	_, err := f.service.GetCredential(f.ctx, f.actor, id)
	require.Equal(t, 404, errs.From(err).HTTPStatus)
	require.Equal(t, 30101, errs.From(err).Code)
	require.Equal(t, before, f.databaseSnapshot(t), "out-of-scope requests must not mutate any credential or audit")
}

func TestCredentialIntegrationT24CreateRequiresItsOwnPermission(t *testing.T) {
	// REQ-IAM-004 / REQ-SHP-003: list permission does not imply write permission.
	f := newCredentialFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	role := identitymodel.Role{TenantID: 1, Code: "credential-reader", Name: "reader", DataScope: "all", Status: "active"}
	require.NoError(t, q.Create(&role).Error)
	require.NoError(t, q.Create(&identitymodel.UserRole{TenantID: 1, UserID: f.user.ID, RoleID: role.ID}).Error)
	var allowed identitymodel.Permission
	require.NoError(t, q.Select("id").Where("code = ?", permission.ShopCredentialList).Take(&allowed).Error)
	require.NoError(t, q.Create(&identitymodel.RolePermission{TenantID: 1, RoleID: role.ID, PermissionID: allowed.ID}).Error)
	cancel()
	f.bearer = f.login(t).AccessToken
	response, envelope := f.request(t, http.MethodPost, f.shopPath()+"/credentials", map[string]any{"kind": "oauth2", "values": map[string]string{"private_value": f.randomValue(t)}})
	require.Equal(t, 403, response.Code)
	require.Equal(t, 20107, envelope.Code)
}

func TestCredentialIntegrationT24CrossTenantIsNotFound(t *testing.T) {
	f := newCredentialFixture(t)
	created, _ := f.create(t, "oauth2")
	// A real tenant 2 actor can hold all scope yet must never see tenant 1's shop.
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("INSERT INTO tenant (id, code, name) OVERRIDING SYSTEM VALUE VALUES (2, 'credential-other', 'other')").Error)
	dept := identitymodel.Department{TenantID: 2, Name: "other", Path: "2", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	user := identitymodel.User{TenantID: 2, Username: "other-tenant", PasswordHash: f.user.PasswordHash, DepartmentID: dept.ID, Status: "active"}
	require.NoError(t, q.Create(&user).Error)
	role := identitymodel.Role{TenantID: 2, Code: "tenant-all", Name: "all", DataScope: "all", Status: "active"}
	require.NoError(t, q.Create(&role).Error)
	require.NoError(t, q.Create(&identitymodel.UserRole{TenantID: 2, UserID: user.ID, RoleID: role.ID}).Error)
	actor := shop.Actor{TenantID: 2, UserID: user.ID, DepartmentID: dept.ID}
	_, err := f.service.GetCredential(f.ctx, actor, created.ID)
	require.Equal(t, 30101, errs.From(err).Code)
	_, err = f.service.ListCredentials(f.ctx, actor, f.shop.ID, credentialTestPage())
	require.Equal(t, 30101, errs.From(err).Code)
	_, err = f.service.DisableCredential(f.ctx, actor, created.ID)
	require.Equal(t, 30101, errs.From(err).Code)
}

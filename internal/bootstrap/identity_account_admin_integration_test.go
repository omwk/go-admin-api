package bootstrap

import (
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func TestIdentityAccountAdminIntegrationForcedChangeAndReset(t *testing.T) {
	f := newAccountFixture(t)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, identity.RegisterAdminRoutes(f.router, f.service, profiles, f.accounts.Admin))
	password := accountPassword(t)
	created := f.request(t, "POST", "/admin/users", f.token, map[string]any{"username": "admin-created", "password": password, "dept_id": f.user.DepartmentID, "status": "active"})
	require.Equal(t, 201, created.Code, created.Body.String())
	var user identity.AdminUser
	notificationDataF3(t, created, &user)
	persisted := accountUser(t, f, user.ID)
	require.True(t, persisted.ForcePasswordChange)
	login, err := f.service.Login(f.ctx, 1, user.Username, password, identity.RequestMeta{})
	require.NoError(t, err)
	require.True(t, login.PasswordChangeRequired)
	require.Equal(t, "password_change_required", login.PasswordStatus)
	require.Equal(t, 403, f.request(t, "GET", "/auth/sessions", login.AccessToken, nil).Code)
	next := accountPassword(t)
	require.Equal(t, 204, f.request(t, "PUT", "/auth/password", login.AccessToken, map[string]string{"oldPassword": password, "newPassword": next}).Code)
	rotated, err := f.service.Refresh(f.ctx, login.RefreshToken, identity.RequestMeta{})
	require.NoError(t, err)
	resetPassword := accountPassword(t)
	reset := f.request(t, "POST", fmt.Sprintf("/admin/users/%d/reset-password", user.ID), f.token, map[string]string{"password": resetPassword})
	require.Equal(t, 204, reset.Code, reset.Body.String())
	f.server.FlushAll()
	_, err = f.service.Authenticate(f.ctx, rotated.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.service.Login(f.ctx, 1, user.Username, next, identity.RequestMeta{})
	require.Equal(t, 20101, errs.From(err).Code)
	login, err = f.service.Login(f.ctx, 1, user.Username, resetPassword, identity.RequestMeta{})
	require.NoError(t, err)
	require.True(t, login.PasswordChangeRequired)
	reused := f.request(t, "POST", fmt.Sprintf("/admin/users/%d/reset-password", user.ID), f.token, map[string]string{"password": password})
	require.Equal(t, 400, reused.Code)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET status='disabled' WHERE id=?", user.ID))
	reset = f.request(t, "POST", fmt.Sprintf("/admin/users/%d/reset-password", user.ID), f.token, map[string]string{"password": accountPassword(t)})
	require.Equal(t, 204, reset.Code, reset.Body.String())
	var status string
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("sys_user").Select("status").Where("id=?", user.ID).Scan(&status).Error)
	require.Equal(t, "disabled", status)
}

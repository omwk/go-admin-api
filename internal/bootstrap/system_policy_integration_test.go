package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestSystemSettingsIntegrationLivePasswordAndLoginPolicy(t *testing.T) {
	// S2 §2.3: subsequent account writes and login attempts read the registered tenant overrides.
	f := newSystemFixture(t, nil)
	response, _ := systemRequest(t, f, "PUT", "/admin/settings/security", map[string]any{"password_min_length": 32, "login_max_failures": 3, "login_lock_minutes": 2}, "")
	require.Equal(t, 200, response.Code)
	r := identity.CreateUserRequest{UserFields: identity.UserFields{DepartmentID: f.user.DepartmentID, Status: "active"}, Username: "policy_short", Password: accountPassword(t)}
	response, _ = adminRequest(t, f.adminFixture, "POST", "/users", r, "")
	require.Equal(t, 400, response.Code)
	response, _ = systemRequest(t, f, "PUT", "/admin/settings/security", map[string]any{"password_min_length": 12, "password_require_mixed": false}, "")
	require.Equal(t, 200, response.Code)
	raw, err := refresh.Generate()
	require.NoError(t, err)
	r.Username, r.Password = "policy_unmixed", strings.Repeat(raw[:1], 12)
	response, _ = adminRequest(t, f.adminFixture, "POST", "/users", r, "")
	require.Equal(t, 201, response.Code, response.Body.String())
	response, _ = systemRequest(t, f, "PUT", "/admin/settings/security", map[string]any{"password_require_mixed": true}, "")
	require.Equal(t, 200, response.Code)
	r.Username = "policy_mixed"
	response, _ = adminRequest(t, f.adminFixture, "POST", "/users", r, "")
	require.Equal(t, 400, response.Code)
	wrong, err := refresh.Generate()
	require.NoError(t, err)
	for _, status := range []int{401, 401, 423} {
		_, err = f.identityFixture.service.Login(f.ctx, 1, f.user.Username, wrong, identity.RequestMeta{})
		require.Equal(t, status, errs.From(err).HTTPStatus)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var stored model.User
	require.NoError(t, q.Select("locked_until").Where("tenant_id=1 AND id=?", f.user.ID).Take(&stored).Error)
	require.NotNil(t, stored.LockedUntil)
	require.WithinDuration(t, f.now.Add(2*time.Minute), *stored.LockedUntil, time.Microsecond)
	f.now = f.now.Add(2 * time.Minute)
	_, err = f.identityFixture.service.Login(f.ctx, 1, f.user.Username, f.password, identity.RequestMeta{})
	require.NoError(t, err)
}

func newSystemAccountFixture(t *testing.T) (*accountFixture, *system.SettingService) {
	t.Helper()
	f := newAccountFixture(t)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	settings, dicts, err := newSystemServices(f.database)
	require.NoError(t, err)
	f.service.UseSettings(settings)
	f.accounts.Admin.UseSettings(settings)
	require.NoError(t, system.RegisterRoutes(f.router, f.service, profiles, settings, dicts, systemWriter(f.accounts.Admin)))
	return f, settings
}

func TestSystemSettingsIntegrationLivePasswordHistory(t *testing.T) {
	// S2 §2.3: changing the history limit takes effect on the next password change.
	f, _ := newSystemAccountFixture(t)
	response := f.request(t, "PUT", "/admin/settings/security", f.token, map[string]any{"password_history_size": 2})
	require.Equal(t, 200, response.Code, response.Body.String())
	first, second := accountPassword(t), accountPassword(t)
	change := func(old, next string) int {
		return f.request(t, "PUT", "/auth/password", f.token, map[string]any{"oldPassword": old, "newPassword": next}).Code
	}
	require.Equal(t, 204, change(f.password, first))
	require.Equal(t, 204, change(first, second))
	require.Equal(t, 400, change(second, first))
	response = f.request(t, "PUT", "/admin/settings/security", f.token, map[string]any{"password_history_size": 0})
	require.Equal(t, 200, response.Code)
	require.Equal(t, 204, change(second, first))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("password_history").Where("tenant_id=1 AND user_id=?", f.user.ID).Count(&count).Error)
	require.Zero(t, count)
}

func TestSystemSettingsIntegrationLivePasswordExpiry(t *testing.T) {
	// S2 §2.3: expiry is checked by login and existing-session middleware; zero disables it.
	f, settings := newSystemAccountFixture(t)
	response := f.request(t, "PUT", "/admin/settings/security", f.token, map[string]any{"password_expire_days": 1})
	require.Equal(t, 200, response.Code)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET password_changed_at=? WHERE tenant_id=1 AND id=?", f.now.Add(-48*time.Hour), f.user.ID))
	tokens, err := f.service.Login(f.ctx, 1, f.user.Username, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	require.True(t, tokens.PasswordExpired)
	response = f.request(t, "GET", "/admin/settings", f.token, nil)
	require.Equal(t, 403, response.Code)
	_, err = settings.Update(f.ctx, system.Actor{TenantID: 1, UserID: f.user.ID}, "security", map[string]json.RawMessage{"password_expire_days": json.RawMessage("0")})
	require.NoError(t, err)
	response = f.request(t, "GET", "/admin/settings", f.token, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	tokens, err = f.service.Login(f.ctx, 1, f.user.Username, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	require.False(t, tokens.PasswordExpired)
}

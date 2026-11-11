package bootstrap

import (
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

func TestIdentityAccountPasswordIntegrationKeepCurrentAndHistory(t *testing.T) {
	// F1 §5.6–7: incorrect old password, complexity, last-five history and family isolation.
	f := newAccountFixture(t)
	current, other := f.login(t), f.login(t)
	claims, err := f.service.Authenticate(f.ctx, current.AccessToken)
	require.NoError(t, err)
	next := accountPassword(t)
	require.Error(t, f.accounts.ChangePassword(f.ctx, claims, accountPassword(t), next))
	for _, invalid := range []string{"short1", "abcdefgh", "12345678"} {
		require.Error(t, f.accounts.ChangePassword(f.ctx, claims, f.password, invalid))
	}
	require.NoError(t, f.accounts.ChangePassword(f.ctx, claims, f.password, next))
	_, err = f.service.Authenticate(f.ctx, current.AccessToken)
	require.NoError(t, err)
	_, err = f.service.Authenticate(f.ctx, other.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	f.server.FlushAll()
	_, err = f.service.Refresh(f.ctx, other.RefreshToken, identity.RequestMeta{})
	require.Equal(t, 20103, errs.From(err).Code)
	passwords := []string{next}
	for i := 0; i < 5; i++ {
		newPassword := accountPassword(t)
		require.NoError(t, f.accounts.ChangePassword(f.ctx, claims, passwords[len(passwords)-1], newPassword))
		passwords = append(passwords, newPassword)
	}
	history, err := f.accounts.Store.PasswordHistory(f.ctx, 1, f.user.ID)
	require.NoError(t, err)
	require.Len(t, history, 5)
	for _, password := range passwords[1:] {
		require.Error(t, f.accounts.ChangePassword(f.ctx, claims, passwords[len(passwords)-1], password))
	}
	require.NoError(t, f.accounts.ChangePassword(f.ctx, claims, passwords[len(passwords)-1], next))
}

func TestIdentityAccountExpiryIntegrationRestrictsAndRecovers(t *testing.T) {
	// F1 §5.8: expiration is a live restriction, so an existing bearer cannot bypass it.
	f := newAccountFixture(t)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET password_changed_at=? WHERE id=?", f.now.Add(-91*24*time.Hour), f.user.ID))
	login := f.request(t, "POST", "/auth/login", "", map[string]any{"account": f.user.Username, "password": f.password})
	require.Equal(t, 200, login.Code, login.Body.String())
	var result identity.Tokens
	notificationDataF3(t, login, &result)
	require.True(t, result.PasswordExpired)
	require.True(t, result.PasswordChangeRequired)
	require.Equal(t, "password_expired", result.PasswordStatus)
	refreshed, err := f.service.Refresh(f.ctx, result.RefreshToken, identity.RequestMeta{})
	require.NoError(t, err)
	require.True(t, refreshed.PasswordExpired)
	require.True(t, refreshed.PasswordChangeRequired)
	result = refreshed
	blocked := f.request(t, "GET", "/auth/sessions", result.AccessToken, nil)
	require.Equal(t, 403, blocked.Code)
	require.Contains(t, blocked.Body.String(), "password_expired")
	next := accountPassword(t)
	changed := f.request(t, "PUT", "/auth/password", result.AccessToken, map[string]any{"oldPassword": f.password, "newPassword": next})
	require.Equal(t, 204, changed.Code, changed.Body.String())
	list := f.request(t, "GET", "/auth/sessions", result.AccessToken, nil)
	require.Equal(t, 200, list.Code, list.Body.String())
}

func TestIdentityAccountSessionsIntegrationOwnDeleteProfileAndLogoutAll(t *testing.T) {
	// F1 §5.11 plus authenticated self-service without admin permissions.
	f := newAccountFixture(t)
	other := f.login(t)
	claims, err := f.service.Authenticate(f.ctx, f.token)
	require.NoError(t, err)
	second, err := f.service.Authenticate(f.ctx, other.AccessToken)
	require.NoError(t, err)
	sessions, err := f.accounts.MySessions(f.ctx, claims, repo.Page{Number: 1, Size: 20})
	require.NoError(t, err)
	require.EqualValues(t, 2, sessions.Total)
	require.NoError(t, f.accounts.DeleteSession(f.ctx, claims, second.SessionID))
	_, err = f.service.Authenticate(f.ctx, other.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	email := accountPassword(t) + "@example.invalid"
	updated := f.request(t, "PUT", "/auth/profile", f.token, map[string]any{"nickname": "account-member", "email": email, "avatar": "/avatars/member"})
	require.Equal(t, 200, updated.Code, updated.Body.String())
	persisted := accountUser(t, f, f.user.ID)
	require.NotNil(t, persisted.Email)
	require.NoError(t, f.accounts.LogoutAll(f.ctx, claims))
	f.server.FlushAll()
	_, err = f.service.Authenticate(f.ctx, f.token)
	require.Equal(t, 20103, errs.From(err).Code)
}

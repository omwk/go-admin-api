package bootstrap

import (
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func TestIdentityAccountRecoveryIntegrationUniformAcceptedAndReset(t *testing.T) {
	// F1 §5.4–5,10: unknown accounts receive the same 202 schema; reset is single use.
	f := newAccountFixture(t)
	oldSession := f.login(t)
	for _, account := range []string{f.user.Username, "missing-member"} {
		response := f.request(t, "POST", "/auth/password/forgot", "", map[string]any{"account": account})
		require.Equal(t, 202, response.Code, response.Body.String())
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		var result identity.RecoveryResult
		notificationDataF3(t, response, &result)
		raw := inviteRaw(t, result.Link)
		newPassword := accountPassword(t)
		reset := f.request(t, "POST", "/auth/password/reset", "", map[string]any{"token": raw, "newPassword": newPassword})
		if account == "missing-member" {
			require.Equal(t, 400, reset.Code)
			continue
		}
		require.Equal(t, 204, reset.Code, reset.Body.String())
		_, err := f.service.Login(f.ctx, 1, account, f.password, identity.RequestMeta{})
		require.Equal(t, 20101, errs.From(err).Code)
		_, err = f.service.Login(f.ctx, 1, account, newPassword, identity.RequestMeta{})
		require.NoError(t, err)
		repeat := f.request(t, "POST", "/auth/password/reset", "", map[string]any{"token": raw, "newPassword": accountPassword(t)})
		require.Equal(t, 400, repeat.Code)
		f.server.FlushAll()
		_, err = f.service.Authenticate(f.ctx, oldSession.AccessToken)
		require.Equal(t, 20103, errs.From(err).Code)
	}
}

func TestIdentityAccountRecoveryIntegrationExpiryProductionAndAbuse(t *testing.T) {
	f := newAccountFixture(t)
	result, err := f.accounts.ForgotPassword(f.ctx, 1, f.user.Username, identity.RequestMeta{IP: "192.0.2.10"})
	require.NoError(t, err)
	raw := inviteRaw(t, result.Link)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE auth_token SET expires_at=? WHERE purpose='reset'", f.now.Add(-time.Second)))
	require.ErrorContains(t, f.accounts.ResetPassword(f.ctx, 1, raw, accountPassword(t), identity.RequestMeta{IP: "192.0.2.10"}), "邀请或链接无效")
	f.accounts.CopyResetLinks = false
	for _, account := range []string{f.user.Username, "missing-member"} {
		response := f.request(t, "POST", "/auth/password/forgot", "", map[string]any{"account": account})
		require.Equal(t, 202, response.Code)
		require.Contains(t, response.Body.String(), `"data":{"accepted":true}`)
		require.NotContains(t, response.Body.String(), "dev_reset_url")
	}
	for i := 0; i < 5; i++ {
		_, err := f.accounts.ForgotPassword(f.ctx, 1, "budget-member", identity.RequestMeta{IP: accountPassword(t)})
		require.NoError(t, err)
	}
	_, err = f.accounts.ForgotPassword(f.ctx, 1, "budget-member", identity.RequestMeta{IP: "192.0.2.11"})
	require.Equal(t, 429, errs.From(err).HTTPStatus)
	for i := 0; i < 5; i++ {
		_, err := f.accounts.ForgotPassword(f.ctx, 1, accountPassword(t), identity.RequestMeta{IP: "192.0.2.12"})
		require.NoError(t, err)
	}
	_, err = f.accounts.ForgotPassword(f.ctx, 1, accountPassword(t), identity.RequestMeta{IP: "192.0.2.12"})
	require.Equal(t, 429, errs.From(err).HTTPStatus)
}

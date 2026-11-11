package bootstrap

import (
	"sync"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestIdentityAccountTokenIntegrationConcurrentConsumption(t *testing.T) {
	f := newAccountFixture(t)
	invite := f.invite(t, 1)
	raw := inviteRaw(t, invite.Link)
	password := accountPassword(t)
	start := make(chan struct{})
	results := make(chan error, 3)
	var workers sync.WaitGroup
	for _, name := range []string{"concurrent-a", "concurrent-b", "concurrent-c"} {
		workers.Add(1)
		go func(name string) {
			defer workers.Done()
			<-start
			_, err := f.accounts.AcceptInvite(f.ctx, 1, identity.AcceptInviteInput{Token: raw, Username: name, Password: password}, identity.RequestMeta{IP: name})
			results <- err
		}(name)
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			require.ErrorContains(t, err, "邀请或链接无效")
		}
	}
	require.Equal(t, 1, winners)
	reset, err := f.accounts.ForgotPassword(f.ctx, 1, f.user.Username, identity.RequestMeta{})
	require.NoError(t, err)
	raw = inviteRaw(t, reset.Link)
	results = make(chan error, 2)
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results <- f.accounts.ResetPassword(f.ctx, 1, raw, password, identity.RequestMeta{})
		}()
	}
	workers.Wait()
	close(results)
	winners = 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			require.ErrorContains(t, err, "邀请或链接无效")
		}
	}
	require.Equal(t, 1, winners)
}

func TestIdentityAccountOwnershipIntegrationTenantAndSessionIsolation(t *testing.T) {
	f := newAccountFixture(t)
	invite := f.invite(t, 1)
	password := accountPassword(t)
	user, err := f.accounts.AcceptInvite(f.ctx, 1, identity.AcceptInviteInput{Token: inviteRaw(t, invite.Link), Username: "second-member", Password: password}, identity.RequestMeta{})
	require.NoError(t, err)
	second, err := f.service.Login(f.ctx, 1, user.Username, password, identity.RequestMeta{})
	require.NoError(t, err)
	claims, err := f.service.Authenticate(f.ctx, f.token)
	require.NoError(t, err)
	foreign, err := jwt.Verify(f.secret, second.AccessToken, f.now)
	require.NoError(t, err)
	err = f.accounts.DeleteSession(f.ctx, claims, foreign.SessionID)
	require.Equal(t, 404, errs.From(err).HTTPStatus)
	_, err = f.service.Authenticate(f.ctx, second.AccessToken)
	require.NoError(t, err)
	w := f.request(t, "GET", "/admin/invites", second.AccessToken, nil)
	require.Equal(t, 403, w.Code)
	w = f.request(t, "GET", "/auth/sessions", second.AccessToken, nil)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), claims.SessionID)
	tenantInvite := f.invite(t, 1)
	_, err = f.accounts.AcceptInvite(f.ctx, 2, identity.AcceptInviteInput{Token: inviteRaw(t, tenantInvite.Link), Username: "cross-tenant", Password: password}, identity.RequestMeta{})
	require.ErrorContains(t, err, "邀请或链接无效")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Model(&model.User{}).Where("tenant_id=2").Count(&count).Error)
	require.Zero(t, count)
}

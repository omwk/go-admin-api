package bootstrap

import (
	"context"
	"sync"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

type gatedSessions struct {
	repo.Auth
	first   sync.Once
	entered chan struct{}
	release chan struct{}
}

func (g *gatedSessions) LockSession(ctx context.Context, tenant int64, sid string) error {
	if err := g.Auth.LockSession(ctx, tenant, sid); err != nil {
		return err
	}
	var err error
	g.first.Do(func() {
		close(g.entered)
		select {
		case <-g.release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}

type refreshResult struct {
	pair identity.Tokens
	err  error
}

func TestIdentityIntegrationRefreshAndLogoutAreSerialized(t *testing.T) {
	// REQ-IAM-010: both lock orders prohibit a live refresh created after logout.
	for _, refreshFirst := range []bool{true, false} {
		name := "logout_first"
		if refreshFirst {
			name = "refresh_first"
		}
		t.Run(name, func(t *testing.T) {
			f := newIdentityFixture(t)
			issued := f.login(t)
			claims, err := f.service.Authenticate(f.ctx, issued.AccessToken)
			require.NoError(t, err)
			gate := &gatedSessions{Auth: f.store, entered: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(gate.release) }) }
			t.Cleanup(release)
			f.useStore(t, gate)
			refreshDone := make(chan refreshResult, 1)
			logoutDone := make(chan error, 1)
			startRefresh := func() {
				pair, err := f.service.Refresh(f.ctx, issued.RefreshToken, identity.RequestMeta{})
				refreshDone <- refreshResult{pair: pair, err: err}
			}
			startLogout := func() { logoutDone <- f.service.Logout(f.ctx, claims) }
			if refreshFirst {
				go startRefresh()
			} else {
				go startLogout()
			}
			select {
			case <-gate.entered:
			case <-f.ctx.Done():
				t.Fatal("session operation did not acquire lock")
			}
			if refreshFirst {
				go startLogout()
			} else {
				go startRefresh()
			}
			release()
			result := <-refreshDone
			require.NoError(t, <-logoutDone)
			if refreshFirst {
				require.NoError(t, result.err)
				_, err = f.service.Authenticate(f.ctx, result.pair.AccessToken)
				require.Equal(t, 20103, errs.From(err).Code)
			} else {
				require.Equal(t, 20103, errs.From(result.err).Code)
			}
			assertLoggedOutFamily(t, f, claims)
		})
	}
}

func assertLoggedOutFamily(t *testing.T, f *identityFixture, claims jwt.Claims) {
	t.Helper()
	family, err := f.store.Family(f.ctx, claims.TenantID, f.user.ID, claims.SessionID)
	require.NoError(t, err)
	for _, row := range family.Tokens {
		require.NotNil(t, row.RevokedAt)
		require.Equal(t, "logout", *row.RevokedReason)
	}
}

package bootstrap

import (
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/session"
	"github.com/stretchr/testify/require"
)

func TestIdentityIntegrationRevocationTTLsUseRemainingLifetime(t *testing.T) {
	// REQ-IAM-010: only refresh is marked during rotation; logout covers the latest family expiry.
	f := newIdentityFixture(t)
	first := f.login(t)
	f.advance(10 * time.Minute)
	next, err := f.service.Refresh(f.ctx, first.RefreshToken, identity.RequestMeta{})
	require.NoError(t, err)
	claims, err := f.service.Authenticate(f.ctx, next.AccessToken)
	require.NoError(t, err)
	accessKey := session.AccessKey(claims.TokenID)
	sessionKey := session.RevocationKey(claims.SessionID)
	oldKey := session.RefreshKey(refresh.Hash(first.RefreshToken))
	nextKey := session.RefreshKey(refresh.Hash(next.RefreshToken))
	require.True(t, f.server.Exists(oldKey))
	require.False(t, f.server.Exists(accessKey))
	require.False(t, f.server.Exists(sessionKey))
	f.advance(time.Minute)
	require.NoError(t, f.service.Logout(f.ctx, claims))
	require.Equal(t, 14*time.Minute, f.server.TTL(accessKey))
	require.Equal(t, refresh.TTL-11*time.Minute, f.server.TTL(oldKey))
	require.Equal(t, refresh.TTL-time.Minute, f.server.TTL(nextKey))
	require.Equal(t, refresh.TTL-time.Minute, f.server.TTL(sessionKey))
	f.advance(time.Minute)
	require.NoError(t, f.service.Logout(f.ctx, claims))
	require.Equal(t, refresh.TTL-2*time.Minute, f.server.TTL(sessionKey))
	_, err = f.service.Authenticate(f.ctx, first.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
}

func (f *identityFixture) advance(duration time.Duration) {
	f.now = f.now.Add(duration)
	f.server.FastForward(duration)
	f.server.SetTime(f.now)
}

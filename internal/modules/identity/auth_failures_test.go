package identity

import (
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestRefreshRedisWriteFailureKeepsCommittedChain(t *testing.T) {
	// REQ-IAM-002: the Redis write is after commit and never exposes a successful result on failure.
	f := newAuthFixture(t)
	first := f.login(t)
	f.redis.failMark = true
	result, err := f.service.Refresh(f.ctx, first.RefreshToken, RequestMeta{})
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	require.Equal(t, Tokens{}, result)
	require.Len(t, f.store.tokens, 2)
	old, found, err := f.store.RefreshByHash(f.ctx, refresh.Hash(first.RefreshToken))
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, old.ReplacedByTokenID)
	require.Equal(t, "rotated", *old.RevokedReason)
	f.redis.failMark = false
	_, err = f.service.Refresh(f.ctx, first.RefreshToken, RequestMeta{})
	require.Equal(t, 20109, errs.From(err).Code)
	f.now = f.now.Add(31 * time.Second)
	f.redis.failMark = true
	_, err = f.service.Refresh(f.ctx, first.RefreshToken, RequestMeta{})
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	for _, row := range f.store.tokens {
		require.Equal(t, "replay", *row.RevokedReason)
	}
}

func TestLogoutRedisWriteFailureKeepsPersistentRevocation(t *testing.T) {
	f := newAuthFixture(t)
	issued := f.login(t)
	claims, err := f.service.Authenticate(f.ctx, issued.AccessToken)
	require.NoError(t, err)
	f.redis.failMark = true
	err = f.service.Logout(f.ctx, claims)
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	for _, row := range f.store.tokens {
		require.Equal(t, "logout", *row.RevokedReason)
	}
	f.redis.failMark = false
	_, err = f.service.Authenticate(f.ctx, issued.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
}

func TestFailedRefreshInsertRollsBackClaim(t *testing.T) {
	f := newAuthFixture(t)
	issued := f.login(t)
	f.store.failInsert = true
	_, err := f.service.Refresh(f.ctx, issued.RefreshToken, RequestMeta{})
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	require.Len(t, f.store.tokens, 1)
	row, _, err := f.store.RefreshByHash(f.ctx, refresh.Hash(issued.RefreshToken))
	require.NoError(t, err)
	require.Nil(t, row.RevokedAt)
	require.Empty(t, f.redis.values)
}

func TestRedisUnavailableFailsClosedAndNaturalExpiryUses20102(t *testing.T) {
	f := newAuthFixture(t)
	issued := f.login(t)
	f.redis.down = true
	_, err := f.service.Authenticate(f.ctx, issued.AccessToken)
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	_, err = f.service.Refresh(f.ctx, issued.RefreshToken, RequestMeta{})
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	_, err = f.service.Login(f.ctx, 1, "operator", f.password, RequestMeta{})
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	require.Len(t, f.store.tokens, 1)
	f.redis.down = false
	f.now = f.now.Add(15 * time.Minute)
	_, err = f.service.Authenticate(f.ctx, issued.AccessToken)
	require.Equal(t, 20102, errs.From(err).Code)
	f.now = f.now.Add(refresh.TTL)
	_, err = f.service.Refresh(f.ctx, issued.RefreshToken, RequestMeta{})
	require.Equal(t, 20102, errs.From(err).Code)
}

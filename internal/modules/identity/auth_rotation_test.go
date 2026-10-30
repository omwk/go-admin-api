package identity

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/session"
	"github.com/stretchr/testify/require"
)

func TestRefreshGraceBoundariesAndReplay(t *testing.T) {
	// REQ-IAM-002/010: the first rotation timestamp is immutable across retries.
	f := newAuthFixture(t)
	pc := f.login(t)
	phone := f.login(t)
	rotatedAt := f.now
	next, err := f.service.Refresh(f.ctx, pc.RefreshToken, RequestMeta{})
	require.NoError(t, err)
	for _, seconds := range []int{0, 29, 30} {
		f.now = rotatedAt.Add(time.Duration(seconds) * time.Second)
		_, err = f.service.Refresh(f.ctx, pc.RefreshToken, RequestMeta{})
		require.Equal(t, 20109, errs.From(err).Code)
		require.Equal(t, 409, errs.From(err).HTTPStatus)
		require.Len(t, f.store.tokens, 3)
	}
	f.now = rotatedAt.Add(31 * time.Second)
	_, err = f.service.Refresh(f.ctx, pc.RefreshToken, RequestMeta{})
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.service.Refresh(f.ctx, next.RefreshToken, RequestMeta{})
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.service.Authenticate(f.ctx, next.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.service.Refresh(f.ctx, phone.RefreshToken, RequestMeta{})
	require.NoError(t, err)
}

func TestKickAndReplayNeverUseRotationGrace(t *testing.T) {
	// REQ-IAM-010: administrative/replay revocation overrides even a rotated old token.
	for _, reason := range []string{"kick", "replay"} {
		t.Run(reason, func(t *testing.T) {
			f := newAuthFixture(t)
			issued := f.login(t)
			_, err := f.service.Refresh(f.ctx, issued.RefreshToken, RequestMeta{})
			require.NoError(t, err)
			row, _, err := f.store.RefreshByHash(f.ctx, refresh.Hash(issued.RefreshToken))
			require.NoError(t, err)
			require.NoError(t, f.store.Transaction(f.ctx, func(ctx context.Context) error {
				return f.store.RevokeFamily(ctx, row.TenantID, row.UserID, row.SessionID, reason, f.now)
			}))
			_, err = f.service.Refresh(f.ctx, issued.RefreshToken, RequestMeta{})
			require.Equal(t, 20103, errs.From(err).Code)
		})
	}
}

func TestConcurrentRefreshHasOneWinner(t *testing.T) {
	// REQ-IAM-002: all losers use the conditional-update concurrent branch.
	f := newAuthFixture(t)
	first := f.login(t)
	start := make(chan struct{})
	results := make(chan error, 8)
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := f.service.Refresh(f.ctx, first.RefreshToken, RequestMeta{})
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			require.Equal(t, 20109, errs.From(err).Code)
		}
	}
	require.Equal(t, 1, winners)
	require.Len(t, f.store.tokens, 2)
}

func TestLogoutPersistsEveryRowAndSurvivesRedisLoss(t *testing.T) {
	// REQ-IAM-010: logout never grants the rotation grace, even after Redis data loss.
	f := newAuthFixture(t)
	pc, phone := f.login(t), f.login(t)
	next, err := f.service.Refresh(f.ctx, pc.RefreshToken, RequestMeta{})
	require.NoError(t, err)
	claims, err := f.service.Authenticate(f.ctx, next.AccessToken)
	require.NoError(t, err)
	require.NoError(t, f.service.Logout(f.ctx, claims))
	for _, raw := range []string{pc.RefreshToken, next.RefreshToken} {
		_, err = f.service.Refresh(f.ctx, raw, RequestMeta{})
		require.Equal(t, 20103, errs.From(err).Code)
	}
	for _, row := range f.store.tokens {
		if row.SessionID == claims.SessionID {
			require.Equal(t, "logout", *row.RevokedReason)
			require.NotNil(t, row.RevokedAt)
		}
	}
	require.Contains(t, f.redis.values, session.AccessKey(claims.TokenID))
	require.Contains(t, f.redis.values, session.RevocationKey(claims.SessionID))
	require.Contains(t, f.redis.values, session.RefreshKey(refresh.Hash(next.RefreshToken)))
	f.redis.values = make(map[string]marker)
	_, err = f.service.Authenticate(f.ctx, pc.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.service.Authenticate(f.ctx, phone.AccessToken)
	require.NoError(t, err)
}

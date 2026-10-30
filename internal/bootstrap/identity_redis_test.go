package bootstrap

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/session"
	"github.com/stretchr/testify/require"
)

func revocationRedis(t *testing.T) (*authRedis, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	cfg := config.Redis{Addr: server.Addr(), PoolSize: 10, MinIdleConns: 1}
	shared, err := cache.Open(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, shared.Close()) })
	adapter, err := newAuthRedis(context.Background(), shared, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adapter.markers.Close()) })
	return adapter, server
}

func TestRevocationNamespacesAndTTLs(t *testing.T) {
	// REQ-IAM-010: the three marker kinds cannot cover each other or reuse TTLs.
	r, server := revocationRedis(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	raw, err := refresh.Generate()
	require.NoError(t, err)
	suffix := refresh.Hash(raw)
	keys := []string{session.AccessKey(suffix), session.RefreshKey(suffix), session.RevocationKey(suffix)}
	ttls := []time.Duration{15 * time.Minute, 7 * 24 * time.Hour, 6 * 24 * time.Hour}
	for i, key := range keys {
		require.NoError(t, r.Mark(ctx, key, now.Add(ttls[i]), now))
	}
	for i, key := range keys {
		require.Equal(t, ttls[i], server.TTL(key))
		value, err := server.Get(key)
		require.NoError(t, err)
		require.Equal(t, "1", value)
	}
	require.NoError(t, r.Mark(ctx, keys[2], now.Add(time.Hour), now))
	require.Equal(t, ttls[2], server.TTL(keys[2]))
	server.FastForward(15 * time.Minute)
	revoked, err := r.Revoked(ctx, keys[0])
	require.NoError(t, err)
	require.False(t, revoked)
	revoked, err = r.Revoked(ctx, keys[1])
	require.NoError(t, err)
	require.True(t, revoked)
	require.True(t, server.Exists(keys[2]))
}

func TestRepeatedConcurrentRevocationRetainsLongestTTL(t *testing.T) {
	r, server := revocationRedis(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	key := session.RevocationKey("shared-suffix")
	var workers sync.WaitGroup
	results := make(chan error, 8)
	for i := 1; i <= 8; i++ {
		workers.Add(1)
		go func(hours int) {
			defer workers.Done()
			results <- r.Mark(ctx, key, now.Add(time.Duration(hours)*time.Hour), now)
		}(i)
	}
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.Equal(t, 8*time.Hour, server.TTL(key))
	require.NoError(t, r.Mark(ctx, "expired", now, now))
	require.NoError(t, r.Mark(ctx, "past", now.Add(-time.Second), now))
	require.False(t, server.Exists("expired"))
	require.False(t, server.Exists("past"))
	require.NoError(t, r.Mark(ctx, "fractional", now.Add(time.Microsecond), now))
	require.Equal(t, time.Millisecond, server.TTL("fractional"))
}

func TestLoginIPLimiterAndRedisFailure(t *testing.T) {
	r, server := revocationRedis(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		allowed, err := r.AllowLogin(ctx, "192.0.2.1")
		require.NoError(t, err)
		require.True(t, allowed)
	}
	allowed, err := r.AllowLogin(ctx, "192.0.2.1")
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, err = r.AllowLogin(ctx, "192.0.2.2")
	require.NoError(t, err)
	require.True(t, allowed)
	server.Close()
	require.Error(t, r.Ping(ctx))
	_, err = r.Revoked(ctx, "key")
	require.Error(t, err)
	require.Error(t, r.Mark(ctx, "key", time.Now().Add(time.Hour), time.Now()))
}

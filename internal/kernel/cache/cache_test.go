package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/stretchr/testify/require"
)

func testCache(t *testing.T) (*Cache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	c, err := Open(context.Background(), config.Redis{Addr: server.Addr(), PoolSize: 4, MinIdleConns: 1})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	return c, server
}

func TestLockMutualExclusionAndSafeRelease(t *testing.T) {
	// REQ-SYS-004: expired owners cannot release another owner's lease.
	c, server := testCache(t)
	ctx := context.Background()
	first, err := c.Lock(ctx, "lease", 1500*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, 1500*time.Millisecond, server.TTL("lease"))
	_, err = c.Lock(ctx, "lease", time.Second)
	require.ErrorIs(t, err, ErrLocked)
	server.FastForward(2 * time.Second)
	second, err := c.Lock(ctx, "lease", time.Second)
	require.NoError(t, err)
	released, err := first.Release(ctx)
	require.NoError(t, err)
	require.False(t, released)
	released, err = second.Release(ctx)
	require.NoError(t, err)
	require.True(t, released)
	require.False(t, server.Exists("lease"))
}

func TestTokenBucketRefillAndConcurrency(t *testing.T) {
	// REQ-SYS-004: one Lua evaluation atomically refills and spends tokens.
	c, server := testCache(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	server.SetTime(now)
	allowed, remaining, retry, err := c.TokenBucket(ctx, "bucket", 2, 2)
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, remaining)
	require.Zero(t, retry)
	allowed, _, _, err = c.TokenBucket(ctx, "bucket", 2, 2)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, remaining, retry, err = c.TokenBucket(ctx, "bucket", 2, 2)
	require.NoError(t, err)
	require.False(t, allowed)
	require.Zero(t, remaining)
	require.Equal(t, 500*time.Millisecond, retry)
	server.SetTime(now.Add(500 * time.Millisecond))
	allowed, _, _, err = c.TokenBucket(ctx, "bucket", 2, 2)
	require.NoError(t, err)
	require.True(t, allowed)
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, _, callErr := c.TokenBucket(ctx, "parallel", 1, 5)
			if callErr != nil {
				t.Error(callErr)
				return
			}
			if ok {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 5, accepted.Load())
}

func TestCacheValidationCancellationAndFailure(t *testing.T) {
	c, server := testCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, c.Ping(ctx))
	_, err := c.Lock(ctx, "lease", time.Second)
	require.Error(t, err)
	_, err = c.Lock(context.Background(), "lease", 0)
	require.Error(t, err)
	_, _, _, err = c.TokenBucket(context.Background(), "bucket", 0, 1)
	require.Error(t, err)
	server.Close()
	require.Error(t, c.Ping(context.Background()))
}

func TestPoolConfigurationAndValidation(t *testing.T) {
	c, _ := testCache(t)
	require.Equal(t, 4, c.client.Options().PoolSize)
	require.Equal(t, 1, c.client.Options().MinIdleConns)
	_, err := Open(context.Background(), config.Redis{Addr: "localhost:6379", PoolSize: 1, MinIdleConns: 2})
	require.Error(t, err)
}

func TestBasicCacheOperations(t *testing.T) {
	c, server := testCache(t)
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "sample", "value", 0))
	value, exists, err := c.Get(ctx, "sample")
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, "value", value)

	updated, err := c.Expire(ctx, "sample", 1500*time.Millisecond)
	require.NoError(t, err)
	require.True(t, updated)
	require.Equal(t, 1500*time.Millisecond, server.TTL("sample"))
	updated, err = c.Expire(ctx, "missing", time.Second)
	require.NoError(t, err)
	require.False(t, updated)

	first, err := c.Incr(ctx, "counter")
	require.NoError(t, err)
	second, err := c.Incr(ctx, "counter")
	require.NoError(t, err)
	require.EqualValues(t, 1, first)
	require.EqualValues(t, 2, second)

	deleted, err := c.Del(ctx, "sample")
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	value, exists, err = c.Get(ctx, "sample")
	require.NoError(t, err)
	require.False(t, exists)
	require.Empty(t, value)

	require.NoError(t, c.Set(ctx, "short-lived", "value", 1500*time.Millisecond))
	require.Equal(t, 1500*time.Millisecond, server.TTL("short-lived"))
	server.FastForward(2 * time.Second)
	_, exists, err = c.Get(ctx, "short-lived")
	require.NoError(t, err)
	require.False(t, exists)
}

func TestCacheOperationsRejectInvalidKeysAndTTLs(t *testing.T) {
	c, _ := testCache(t)
	ctx := context.Background()
	_, _, err := c.Get(ctx, "")
	require.Error(t, err)
	require.Error(t, c.Set(ctx, "key", "value", -time.Second))
	require.Error(t, c.Set(ctx, "key", "value", time.Nanosecond))
	_, err = c.Del(ctx, "")
	require.Error(t, err)
	_, err = c.Incr(ctx, "")
	require.Error(t, err)
	_, err = c.Expire(ctx, "key", 0)
	require.Error(t, err)
}

func TestCacheOperationsPropagateCancellationAndDependencyFailure(t *testing.T) {
	c, server := testCache(t)
	operations := map[string]func(context.Context) error{
		"get":    func(ctx context.Context) error { _, _, err := c.Get(ctx, "key"); return err },
		"set":    func(ctx context.Context) error { return c.Set(ctx, "key", "value", time.Second) },
		"del":    func(ctx context.Context) error { _, err := c.Del(ctx, "key"); return err },
		"incr":   func(ctx context.Context) error { _, err := c.Incr(ctx, "key"); return err },
		"expire": func(ctx context.Context) error { _, err := c.Expire(ctx, "key", time.Second); return err },
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, operation := range operations {
		t.Run(name+" canceled", func(t *testing.T) { require.Error(t, operation(canceled)) })
	}
	server.Close()
	for name, operation := range operations {
		t.Run(name+" unavailable", func(t *testing.T) { require.Error(t, operation(context.Background())) })
	}
}

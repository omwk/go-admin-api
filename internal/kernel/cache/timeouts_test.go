package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type deadlineHook struct{ deadlines []time.Time }

func (h *deadlineHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *deadlineHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		deadline, _ := ctx.Deadline()
		h.deadlines = append(h.deadlines, deadline)
		return next(ctx, cmd)
	}
}

func (h *deadlineHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		deadline, _ := ctx.Deadline()
		h.deadlines = append(h.deadlines, deadline)
		return next(ctx, cmds)
	}
}

func TestCacheOperationTimeoutsAndEarlierParentDeadline(t *testing.T) {
	for _, budget := range []time.Duration{0, 5 * time.Second} {
		server := miniredis.RunT(t)
		c, err := Open(context.Background(), config.Redis{Addr: server.Addr(), PoolSize: 4}, config.Cache{OpTimeout: budget})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, c.Close()) })
		want := budget
		if want == 0 {
			want = time.Second
		}
		options := c.client.Options()
		for _, got := range []time.Duration{options.DialTimeout, options.ReadTimeout, options.WriteTimeout, options.PoolTimeout} {
			require.Equal(t, want, got)
		}
		require.Equal(t, 0, options.MaxRetries)
		checkCacheOperationDeadlines(t, c, want)
	}
}

func checkCacheOperationDeadlines(t *testing.T, c *Cache, budget time.Duration) {
	t.Helper()
	hook := &deadlineHook{}
	c.client.AddHook(hook)
	ctx := context.Background()
	lease, err := c.Lock(ctx, "timeout-lease", time.Minute)
	require.NoError(t, err)
	operations := []func(context.Context) error{
		c.Ping,
		func(ctx context.Context) error { return c.Set(ctx, "key", "1", 0) },
		func(ctx context.Context) error { _, _, err := c.Get(ctx, "key"); return err },
		func(ctx context.Context) error { _, err := c.Incr(ctx, "key"); return err },
		func(ctx context.Context) error { _, err := c.Expire(ctx, "key", time.Minute); return err },
		func(ctx context.Context) error { _, err := c.Del(ctx, "key"); return err },
		func(ctx context.Context) error { _, _, _, err := c.TokenBucket(ctx, "bucket", 1, 2); return err },
		func(ctx context.Context) error { return c.DeferBucket(ctx, "bucket", time.Second) },
		func(ctx context.Context) error { _, _, err := c.Version(ctx, "version"); return err },
		func(ctx context.Context) error { return c.IncrBatch(ctx, []string{"version"})[0].Err },
		func(ctx context.Context) error { _, err := lease.Renew(ctx, time.Minute); return err },
		func(ctx context.Context) error { _, err := lease.Release(ctx); return err },
	}
	for _, operation := range operations {
		hook.deadlines = nil
		before := time.Now()
		require.NoError(t, operation(ctx))
		require.NotEmpty(t, hook.deadlines)
		for _, deadline := range hook.deadlines {
			require.False(t, deadline.Before(before.Add(budget)))
			require.False(t, deadline.After(time.Now().Add(budget)))
		}
	}
	parent, stop := context.WithTimeout(ctx, 500*time.Millisecond)
	defer stop()
	hook.deadlines = nil
	require.NoError(t, c.Ping(parent))
	deadline, _ := parent.Deadline()
	require.Equal(t, []time.Time{deadline}, hook.deadlines)
}

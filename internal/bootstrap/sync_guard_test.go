package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/stretchr/testify/require"
)

func TestReplayRedisLeaseExpiresWithinHandoffBudget(t *testing.T) {
	redis, clock := revocationRedis(t)
	ctx := context.WithValue(context.Background(), grantKey{}, schedulingGrant{Class: "replay"})
	job := repo.Job{TenantID: 1, ShopID: 7, Resource: "product"}
	require.NoError(t, syncGuard(redis.cache)(ctx, job, func(ctx context.Context) error {
		clock.FastForward(time.Second)
		// Give a renewal ticker an opportunity to extend the crashed owner's lease.
		time.Sleep(750 * time.Millisecond)
		clock.FastForward(time.Second)
		next, err := redis.cache.Lock(ctx, "lock:sync:1:7:product", 30*time.Second)
		require.NoError(t, err)
		released, err := next.Release(ctx)
		require.NoError(t, err)
		require.True(t, released)
		return nil
	}))
}

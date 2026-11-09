package bootstrap

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayTokenBucketsAtomicAndApplicationIsolation(t *testing.T) {
	// REQ-GW-005: independent instances, keys and routes share one application bucket.
	server := miniredis.RunT(t)
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	server.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	policy := config.Gateway{GlobalQPS: 100, GlobalBurst: 100, ClientQPS: 1, ClientBurst: 2, RouteQPS: 100, RouteBurst: 100}
	first, err := newGatewayLimiter(client, policy)
	require.NoError(t, err)
	second, err := newGatewayLimiter(client, policy)
	require.NoError(t, err)
	p := gateway.Principal{TenantID: 1, ClientID: 7, SecretID: 1}
	_, err = first.Take(context.Background(), p, 11)
	require.NoError(t, err)
	p.SecretID = 2
	_, err = second.Take(context.Background(), p, 12)
	require.NoError(t, err)
	before, err := client.HGet(context.Background(), "gateway:rl:global", "tokens").Result()
	require.NoError(t, err)
	decision, err := first.Take(context.Background(), p, 11)
	var limited *gateway.LimitError
	require.ErrorAs(t, err, &limited)
	require.Equal(t, "client", decision.LimitType)
	require.Equal(t, time.Second, decision.RetryAfter)
	after, err := client.HGet(context.Background(), "gateway:rl:global", "tokens").Result()
	require.NoError(t, err)
	require.Equal(t, before, after, "rejection must not partially consume any other bucket")
	p.ClientID = 8
	_, err = second.Take(context.Background(), p, 11)
	require.NoError(t, err)
	server.SetTime(now.Add(time.Second))
	p.ClientID = 7
	_, err = second.Take(context.Background(), p, 11)
	require.NoError(t, err)
	server.SetError("injected Redis failure")
	_, err = first.Take(context.Background(), p, 11)
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	require.Equal(t, 10002, errs.From(err).Code)
}

func TestGatewayTokenBucketsLongestBlockingWaitAndIdleTTL(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC))
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	policy := config.Gateway{GlobalQPS: 1, GlobalBurst: 1, ClientQPS: 10, ClientBurst: 1, RouteQPS: 100, RouteBurst: 1}
	limiter, err := newGatewayLimiter(client, policy)
	require.NoError(t, err)
	p := gateway.Principal{TenantID: 1, ClientID: 1}
	_, err = limiter.Take(context.Background(), p, 1)
	require.NoError(t, err)
	decision, err := limiter.Take(context.Background(), p, 1)
	require.Error(t, err)
	require.Equal(t, "global", decision.LimitType)
	require.Equal(t, time.Second, decision.RetryAfter)
	ttl, err := client.PTTL(context.Background(), "gateway:rl:global").Result()
	require.NoError(t, err)
	require.Equal(t, 2*time.Second, ttl)
	server.FastForward(3 * time.Second)
	_, err = limiter.Take(context.Background(), p, 1)
	require.NoError(t, err)
}

func TestGatewayTokenBucketsConcurrentAdmission(t *testing.T) {
	// REQ-GW-005: a fixed Redis clock makes the shared burst bound deterministic.
	server := miniredis.RunT(t)
	server.SetTime(time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC))
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	policy := config.Gateway{GlobalQPS: 100, GlobalBurst: 100, ClientQPS: 1, ClientBurst: 7, RouteQPS: 100, RouteBurst: 100}
	first, err := newGatewayLimiter(client, policy)
	require.NoError(t, err)
	second, err := newGatewayLimiter(client, policy)
	require.NoError(t, err)
	instances := []*gatewayLimiter{first, second}
	results := make(chan error, 24)
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			p := gateway.Principal{TenantID: 1, ClientID: 7, SecretID: int64(i%2 + 1)}
			_, err := instances[i%2].Take(context.Background(), p, int64(i%2+1))
			results <- err
		}()
	}
	workers.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
			continue
		}
		var limited *gateway.LimitError
		require.True(t, errors.As(err, &limited), "unexpected admission error: %v", err)
		require.Equal(t, "client", limited.Decision.LimitType)
		rejected++
	}
	require.Equal(t, 7, accepted)
	require.Equal(t, 17, rejected)
}

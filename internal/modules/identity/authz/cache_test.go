package authz

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/stretchr/testify/require"
)

func versionCache(t *testing.T) (*Cache, *cache.Cache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	backend, err := cache.Open(context.Background(), config.Redis{Addr: server.Addr(), PoolSize: 10, MinIdleConns: 1})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, backend.Close()) })
	return New(backend), backend, server
}

func TestVersionedPayloadMissingVersionAndTTL(t *testing.T) {
	// REQ-IAM-009: a missing generation cannot reuse v0 or a residual old generation.
	c, backend, server := versionCache(t)
	ctx := context.Background()
	require.NoError(t, server.Set("authz:perm:1:2:v0", `{"version":"0","data":"stale"}`))
	load := func(context.Context) ([]byte, error) { return []byte(`"current"`), nil }
	data, err := c.Load(ctx, 1, 2, "perm", load)
	require.NoError(t, err)
	require.JSONEq(t, `"current"`, string(data))
	version, _, err := backend.Version(ctx, VersionKey(1, 2))
	require.NoError(t, err)
	require.Zero(t, server.TTL(VersionKey(1, 2)))
	key := fmt.Sprintf("authz:perm:1:2:v%s", version)
	require.Equal(t, 5*time.Minute, server.TTL(key))
	raw, err := server.Get(key)
	require.NoError(t, err)
	require.Contains(t, raw, `"version":"`+version+`"`)
	_, err = backend.Del(ctx, VersionKey(1, 2))
	require.NoError(t, err)
	data, err = c.Load(ctx, 1, 2, "perm", func(context.Context) ([]byte, error) { return []byte(`"after-reset"`), nil })
	require.NoError(t, err)
	require.JSONEq(t, `"after-reset"`, string(data))
	version, _, err = backend.Version(ctx, VersionKey(1, 2))
	require.NoError(t, err)
	require.NoError(t, server.Set(fmt.Sprintf("authz:perm:1:2:v%s", version), `{"version":"wrong","data":"stale"}`))
	data, err = c.Load(ctx, 1, 2, "perm", load)
	require.NoError(t, err)
	require.JSONEq(t, `"current"`, string(data))
}

func TestSlowReaderCannotFillNewGeneration(t *testing.T) {
	c, _, _ := versionCache(t)
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := c.Load(ctx, 1, 2, "perm", func(context.Context) ([]byte, error) { close(started); <-release; return []byte(`"old"`), nil })
		finished <- err
	}()
	<-started
	require.False(t, c.Pending(ctx, 1, []int64{2, 2}))
	data, err := c.Load(ctx, 1, 2, "perm", func(context.Context) ([]byte, error) { return []byte(`"new"`), nil })
	require.NoError(t, err)
	require.JSONEq(t, `"new"`, string(data))
	close(release)
	require.NoError(t, <-finished)
	data, err = c.Load(ctx, 1, 2, "perm", func(context.Context) ([]byte, error) { return nil, errors.New("unexpected cold read") })
	require.NoError(t, err)
	require.JSONEq(t, `"new"`, string(data))
}

type batchProbe struct {
	Backend
	mu                          sync.Mutex
	calls                       map[string]int
	active, maxActive, maxBatch int
	failOnce, alwaysFail        bool
}

func (p *batchProbe) IncrBatch(_ context.Context, keys []string) []cache.IncrementResult {
	p.mu.Lock()
	p.active++
	p.maxActive = max(p.maxActive, p.active)
	p.maxBatch = max(p.maxBatch, len(keys))
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.active--; p.mu.Unlock() }()
	results := make([]cache.IncrementResult, len(keys))
	for i, key := range keys {
		p.mu.Lock()
		p.calls[key]++
		attempt := p.calls[key]
		p.mu.Unlock()
		results[i] = cache.IncrementResult{Key: key, Version: 1}
		if p.alwaysFail || p.failOnce && attempt == 1 {
			results[i].Err = errors.New("injected increment failure")
		}
	}
	return results
}

func TestInvalidationDeduplicatesBatchesAndRetriesOnlyFailedKeys(t *testing.T) {
	probe := &batchProbe{calls: map[string]int{}, failOnce: true}
	c := New(probe)
	users := []int64{}
	for i := int64(1); i <= 2000; i++ {
		users = append(users, i, i)
	}
	results := c.Invalidate(context.Background(), 1, users)
	require.Len(t, results, 2000)
	require.LessOrEqual(t, probe.maxActive, 2)
	require.Equal(t, 500, probe.maxBatch)
	for _, result := range results {
		require.NoError(t, result.Err)
		require.Equal(t, 2, probe.calls[result.Key])
	}
	probe = &batchProbe{calls: map[string]int{}, alwaysFail: true}
	c = New(probe)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results = c.Invalidate(ctx, 1, []int64{2, 2})
	require.Len(t, results, 1)
	require.Error(t, results[0].Err)
	require.Equal(t, 1, probe.calls[VersionKey(1, 2)])
}

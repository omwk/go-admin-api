package authz

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var invalidationFailures = promauto.NewCounter(prometheus.CounterOpts{Name: "authz_invalidate_failed_total", Help: "Authorization version keys still pending after bounded invalidation."})

// Invalidate deduplicates users, uses at most two workers and retries failed keys once within budget.
// The returned results retain every key; callers keep the committed business response on failure.
func (c *Cache) Invalidate(ctx context.Context, tenant int64, users []int64) []cache.IncrementResult {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	keys, seen := []string{}, map[int64]bool{}
	for _, user := range users {
		if user > 0 && !seen[user] {
			seen[user] = true
			keys = append(keys, VersionKey(tenant, user))
		}
	}
	results := make([]cache.IncrementResult, len(keys))
	jobs := make(chan int, (len(keys)+499)/500)
	for start := 0; start < len(keys); start += 500 {
		jobs <- start
	}
	close(jobs)
	var workers sync.WaitGroup
	for i := 0; i < min(2, (len(keys)+499)/500); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for start := range jobs {
				end := min(start+500, len(keys))
				batch := c.backend.IncrBatch(ctx, keys[start:end])
				batch = c.retry(ctx, batch)
				copy(results[start:end], batch)
			}
		}()
	}
	workers.Wait()
	for _, result := range results {
		if result.Err != nil {
			invalidationFailures.Inc()
			slog.WarnContext(ctx, "authorization invalidation pending", "trace_id", logger.TraceID(ctx), "module", "identity", "action", "authz_invalidate", "tenant_id", tenant)
		}
	}
	return results
}

func (c *Cache) retry(ctx context.Context, results []cache.IncrementResult) []cache.IncrementResult {
	if ctx.Err() != nil {
		return results
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) < 10*time.Millisecond {
		return results
	}
	failed, positions := []string{}, []int{}
	for i, result := range results {
		if result.Err != nil {
			failed = append(failed, result.Key)
			positions = append(positions, i)
		}
	}
	if len(failed) == 0 {
		return results
	}
	for i, result := range c.backend.IncrBatch(ctx, failed) {
		results[positions[i]] = result
	}
	return results
}

// Pending performs invalidation and reports whether the response needs the pending header.
func (c *Cache) Pending(ctx context.Context, tenant int64, users []int64) bool {
	for _, result := range c.Invalidate(ctx, tenant, users) {
		if result.Err != nil {
			return true
		}
	}
	return false
}

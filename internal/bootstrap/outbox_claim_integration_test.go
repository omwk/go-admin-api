package bootstrap

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/stretchr/testify/require"
)

type claimPublisher struct {
	calls             atomic.Int64
	insideTransaction atomic.Bool
}

func (p *claimPublisher) Publish(ctx context.Context, _ outbox.Event) error {
	p.calls.Add(1)
	if db.InTransaction(ctx) {
		p.insideTransaction.Store(true)
	}
	return nil
}

func TestOutboxIntegrationConcurrentClaimAndExhaustion(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	id := emitAuthorization(ctx, t, database)
	p := &claimPublisher{}
	var alerts atomic.Int64
	d, err := outbox.NewDispatcher(database, p, func(context.Context, outbox.Event, error) { alerts.Add(1) })
	require.NoError(t, err)
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- d.RunOnce(ctx) }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, int64(1), p.calls.Load())
	require.False(t, p.insideTransaction.Load())
	state, attempts := eventState(ctx, t, database, id)
	require.Equal(t, "processing", state) // Enqueue is never completion.
	require.Equal(t, 1, attempts)
	require.Error(t, database.Transaction(ctx, func(tx context.Context) error { return d.RunOnce(tx) }))
}

func TestOutboxIntegrationFinalCrashAlertsOnceWithoutRedelivery(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	id := emitAuthorization(ctx, t, database)
	p := &claimPublisher{}
	var alerts atomic.Int64
	d, err := outbox.NewDispatcher(database, p, func(context.Context, outbox.Event, error) { alerts.Add(1) })
	require.NoError(t, err)
	require.NoError(t, database.Exec(ctx, "UPDATE outbox_event SET status = 'processing', attempts = 10, available_at = ? WHERE id = ?", time.Now().Add(-time.Second), id))
	require.NoError(t, d.RunOnce(ctx))
	require.NoError(t, d.RunOnce(ctx))
	state, attempts := eventState(ctx, t, database, id)
	require.Equal(t, "failed", state)
	require.Equal(t, 10, attempts)
	require.Equal(t, int64(1), alerts.Load())
	require.Zero(t, p.calls.Load())
}

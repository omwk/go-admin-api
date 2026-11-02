package worker

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/stretchr/testify/require"
)

func testQueueConfig(t *testing.T) (config.Redis, config.Worker) {
	t.Helper()
	r := miniredis.RunT(t)
	return config.Redis{Addr: r.Addr(), PoolSize: 10}, config.Worker{Concurrency: 1, Queues: []string{"sync", "refresh", "billing", "notify"}}
}

func TestQueueRejectsForgedIdentifiers(t *testing.T) {
	r, cfg := testQueueConfig(t)
	log, err := logger.New(io.Discard, "info", "json")
	require.NoError(t, err)
	var called atomic.Int64
	srv, err := NewQueueServer(r, cfg, log, func(context.Context, int64, int) error { called.Add(1); return nil },
		func(context.Context, TaskPayload) error { called.Add(1); return nil })
	require.NoError(t, err)
	for _, raw := range []string{
		`{"job_id":1,"trigger":"cron","tenant_id":1}`,
		`{"job_id":1,"trigger":"cron","shop_id":2}`,
		`{"trigger":"cron"}`, `{"job_id":1,"trigger":"cron","scope":{}}`,
		`{"job_id":1,"run_id":-1,"trigger":"cron"}`,
	} {
		err := srv.mux.ProcessTask(context.Background(), asynq.NewTask(SyncTask, []byte(raw)))
		require.ErrorIs(t, err, asynq.SkipRetry)
	}
	for _, raw := range []string{`{"event_id":1,"attempt":1,"tenant_id":1}`, `{"event_id":1}`, `{"event_id":1,"attempt":1} {}`} {
		require.ErrorIs(t, srv.mux.ProcessTask(context.Background(), asynq.NewTask(OutboxTask, []byte(raw))), asynq.SkipRetry)
	}
	require.Zero(t, called.Load())
	payload, err := DecodeTaskPayload([]byte(`{"job_id":1,"run_id":2,"trigger":"retry"}`))
	require.NoError(t, err)
	require.Equal(t, int64(2), payload.RunID)
}

func TestQueueShutdownRequeuesInterruptedTask(t *testing.T) {
	r, cfg := testQueueConfig(t)
	log, err := logger.New(io.Discard, "info", "json")
	require.NoError(t, err)
	started, interrupted := make(chan struct{}), make(chan struct{})
	_, err = newQueueServer(r, cfg, log, func(context.Context, int64, int) error { return nil }, nil, time.Second)
	require.Error(t, err) // A server cannot silently install an empty handler.
	first, err := newQueueServer(r, cfg, log, func(ctx context.Context, _ int64, _ int) error {
		close(started)
		<-ctx.Done()
		close(interrupted)
		return ctx.Err()
	}, func(context.Context, TaskPayload) error { return errors.New("not a sync test") }, 100*time.Millisecond)
	require.NoError(t, err)
	client := asynq.NewClient(RedisOptions(r))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	_, err = client.Enqueue(asynq.NewTask(OutboxTask, []byte(`{"event_id":1,"attempt":1}`)), asynq.Queue("notify"))
	require.NoError(t, err)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- first.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("task was not consumed")
	}
	start := time.Now()
	stop()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown exceeded deadline")
	}
	require.Less(t, time.Since(start), 30*time.Second)
	select {
	case <-interrupted:
	case <-time.After(time.Second):
		t.Fatal("handler was not cancelled")
	}
	inspector := asynq.NewInspector(RedisOptions(r))
	t.Cleanup(func() { require.NoError(t, inspector.Close()) })
	info, err := inspector.GetQueueInfo("notify")
	require.NoError(t, err)
	require.Equal(t, 1, info.Pending)
	require.Zero(t, info.Completed)
	consumed := make(chan struct{}, 1)
	second, err := NewQueueServer(r, cfg, log, func(context.Context, int64, int) error { consumed <- struct{}{}; return nil },
		func(context.Context, TaskPayload) error { return errors.New("not a sync test") })
	require.NoError(t, err)
	ctx2, stop2 := context.WithCancel(context.Background())
	defer stop2()
	go func() { done <- second.Run(ctx2) }()
	select {
	case <-consumed:
	case <-time.After(5 * time.Second):
		t.Fatal("restart did not reclaim task")
	}
	stop2()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("restart shutdown failed")
	}
}

func TestTwoSchedulersEnqueueOneCronSlotAndRestart(t *testing.T) {
	r, _ := testQueueConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	shared, err := cache.Open(ctx, r)
	require.NoError(t, err)
	defer func() { require.NoError(t, shared.Close()) }()
	p := NewQueuePublisher(r)
	defer func() { require.NoError(t, p.Close()) }()
	var firstTicks, secondTicks atomic.Int64
	source := func(context.Context) ([]Schedule, error) {
		return []Schedule{{JobID: 1, Cron: "* * * * *"}}, nil
	}
	fixed := time.Date(2026, 11, 3, 12, 1, 15, 0, time.UTC)
	first, err := NewLeader(shared, "test:leader", 2*time.Second)
	require.NoError(t, err)
	second, err := NewLeader(shared, "test:leader", 2*time.Second)
	require.NoError(t, err)
	done := make(chan error, 2)
	tick := func(ctx context.Context) error { return p.Schedule(ctx, source, fixed) }
	go func() {
		done <- first.RunScheduled(ctx, func(ctx context.Context) error { firstTicks.Add(1); return tick(ctx) })
	}()
	go func() {
		done <- second.RunScheduled(ctx, func(ctx context.Context) error { secondTicks.Add(1); return tick(ctx) })
	}()
	time.Sleep(1600 * time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	require.NoError(t, <-done)
	require.Positive(t, firstTicks.Load()+secondTicks.Load())
	require.True(t, firstTicks.Load() == 0 || secondTicks.Load() == 0, "only one instance may run cron during the lease")
	inspector := asynq.NewInspector(RedisOptions(r))
	defer func() { require.NoError(t, inspector.Close()) }()
	info, err := inspector.GetQueueInfo("sync")
	require.NoError(t, err)
	require.Equal(t, 1, info.Pending)
	acquired, err := second.Acquire(context.Background())
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, tick(context.Background()))
	require.NoError(t, second.Release(context.Background()))
	info, err = inspector.GetQueueInfo("sync")
	require.NoError(t, err)
	require.Equal(t, 1, info.Pending)
}

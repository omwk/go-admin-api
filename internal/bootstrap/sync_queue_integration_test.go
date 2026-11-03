package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/worker"
	"github.com/stretchr/testify/require"
)

func TestSyncIntegrationQueueConsumeSent(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 100))
	run := f.run(t)
	redis := config.Redis{Addr: f.server.Addr(), PoolSize: 10}
	publisher := worker.NewQueuePublisher(redis)
	defer func() { require.NoError(t, publisher.Close()) }()
	dispatcher, err := outbox.NewDispatcher(f.database, publisher, nil)
	require.NoError(t, err)
	consumer, err := outbox.NewConsumer(dispatcher, f.redis.cache, syncEventHandler(f.engine, authorizationEventHandler(f.redis.cache)))
	require.NoError(t, err)
	var logs lockedLog
	log, err := logger.New(&logs, "info", "json")
	require.NoError(t, err)
	server, err := worker.NewQueueServer(redis, config.Worker{Concurrency: 2, Queues: []string{"sync", "refresh", "billing", "notify"}}, log, consumer.Handle,
		func(context.Context, worker.TaskPayload) error { return errors.New("unexpected cron invocation") })
	require.NoError(t, err)
	ctx, stop := context.WithCancel(f.ctx)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	id := syncDispatchID(t, f, run.ID)
	require.NoError(t, dispatcher.RunOnce(f.ctx))
	require.Eventually(t, func() bool { state, _ := eventState(f.ctx, t, f.database, id); return state == "sent" }, 8*time.Second, 20*time.Millisecond)
	stop()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("sync worker shutdown exceeded deadline")
	}
	require.Equal(t, "success", f.readRun(t, run.ID).Status)
	require.Equal(t, "100", f.cursor(t))
	rawCount(f.ctx, t, f.database, "listing", 100)
	assertTaskTrace(t, logs.text())
	f.noLeaks(t, "worker log", logs.text())
}

func TestSyncIntegrationInterruptedDeliveryReclaimed(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 100))
	p := observeSyncPlatform(t, f)
	run := f.run(t)
	captured := &capturedPublisher{}
	dispatcher, err := outbox.NewDispatcher(f.database, captured, nil)
	require.NoError(t, err)
	require.NoError(t, dispatcher.RunOnce(f.ctx))
	require.Len(t, captured.events, 1)
	event := captured.events[0]
	consumer, err := outbox.NewConsumer(dispatcher, f.redis.cache, syncEventHandler(f.engine, authorizationEventHandler(f.redis.cache)))
	require.NoError(t, err)
	ctx, interrupt := context.WithCancel(f.ctx)
	p.fetch = interrupt
	require.Error(t, consumer.Handle(ctx, event.ID, event.Attempts))
	state, _ := eventState(f.ctx, t, f.database, event.ID)
	require.Equal(t, "processing", state)
	rawCount(f.ctx, t, f.database, "listing", 0)
	rawCount(f.ctx, t, f.database, "sync_cursor", 0)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE outbox_event SET available_at=now()-interval '1 second' WHERE id=?", event.ID))
	f.rebuild(t)
	restarted, err := outbox.NewConsumer(dispatcher, f.redis.cache, syncEventHandler(f.engine, authorizationEventHandler(f.redis.cache)))
	require.NoError(t, err)
	require.NoError(t, dispatcher.RunOnce(f.ctx))
	require.Len(t, captured.events, 2)
	retry := captured.events[1]
	require.Equal(t, event.Attempts+1, retry.Attempts)
	require.NoError(t, restarted.Handle(f.ctx, event.ID, event.Attempts))
	state, _ = eventState(f.ctx, t, f.database, event.ID)
	require.Equal(t, "processing", state)
	require.NoError(t, restarted.Handle(f.ctx, retry.ID, retry.Attempts))
	state, _ = eventState(f.ctx, t, f.database, event.ID)
	require.Equal(t, "sent", state)
	require.Equal(t, "success", f.readRun(t, run.ID).Status)
	rawCount(f.ctx, t, f.database, "listing", 100)
	rawCount(f.ctx, t, f.database, "sync_run", 1)
}

func syncDispatchID(t *testing.T, f *syncFixture, runID int64) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var id int64
	require.NoError(t, q.Table("outbox_event").Select("id").Where("event_type='sync.dispatch' AND aggregate_id=?", itoa(runID)).Take(&id).Error)
	return id
}

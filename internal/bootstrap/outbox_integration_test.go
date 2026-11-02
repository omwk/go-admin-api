package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
	"github.com/omwk/go-admin-api/internal/worker"
	"github.com/stretchr/testify/require"
)

type lockedLog struct {
	sync.Mutex
	bytes.Buffer
}

func (b *lockedLog) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}
func (b *lockedLog) text() string { b.Lock(); defer b.Unlock(); return b.Buffer.String() }

func emitAuthorization(ctx context.Context, t *testing.T, database *db.DB) int64 {
	t.Helper()
	require.NoError(t, database.Transaction(ctx, func(tx context.Context) error {
		return outbox.Emit(tx, database, outbox.Event{TenantID: 1, AggregateType: "authorization", AggregateID: "1", EventType: "authorization.invalidate", Payload: []byte(`{"user_ids":[7]}`)})
	}))
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var id int64
	require.NoError(t, q.Table("outbox_event").Select("max(id)").Scan(&id).Error)
	return id
}

func eventState(ctx context.Context, t *testing.T, database *db.DB, id int64) (string, int) {
	t.Helper()
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var row struct {
		Status   string
		Attempts int
	}
	require.NoError(t, q.Table("outbox_event").Select("status, attempts").Where("id = ?", id).Take(&row).Error)
	return row.Status, row.Attempts
}

func TestWorkerIntegrationEnqueueConsumeSent(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	r := miniredis.RunT(t)
	cfg := &config.Config{Redis: config.Redis{Addr: r.Addr(), PoolSize: 10}, Worker: config.Worker{Concurrency: 2, Queues: []string{"sync", "refresh", "billing", "notify"}}}
	shared, err := cache.Open(ctx, cfg.Redis)
	require.NoError(t, err)
	defer func() { require.NoError(t, shared.Close()) }()
	require.NoError(t, shared.Set(ctx, authz.VersionKey(1, 7), "41", 0))
	var logs lockedLog
	log, err := logger.New(&logs, "info", "json")
	require.NoError(t, err)
	id := emitAuthorization(ctx, t, database)
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- RunWorker(runCtx, cfg, database, shared, log) }()
	require.Eventually(t, func() bool { state, _ := eventState(ctx, t, database, id); return state == "sent" }, 8*time.Second, 20*time.Millisecond)
	version, found, err := shared.Get(ctx, authz.VersionKey(1, 7))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "42", version)
	start := time.Now()
	stop()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("worker failed to stop")
	}
	require.Less(t, time.Since(start), 30*time.Second)
	assertTaskTrace(t, logs.text())
	require.NotContains(t, logs.text(), "user_ids")
	t.Log("asynq enqueue -> worker consume -> outbox sent; task log has trace_id")
}

func assertTaskTrace(t *testing.T, logs string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry["action"] == worker.OutboxTask {
			require.NotEmpty(t, entry["trace_id"])
			require.Equal(t, true, entry["success"])
			return
		}
	}
	t.Fatal("no outbox task processing log")
}

type capturedPublisher struct{ events []outbox.Event }

func (p *capturedPublisher) Publish(_ context.Context, event outbox.Event) error {
	p.events = append(p.events, event)
	return nil
}

func TestOutboxIntegrationRollbackRedisFailureAndDurableDedup(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	rollback := errors.New("rollback requested")
	require.ErrorIs(t, database.Transaction(ctx, func(tx context.Context) error {
		require.NoError(t, outbox.Emit(tx, database, outbox.Event{TenantID: 1, AggregateType: "authorization", AggregateID: "1", EventType: "authorization.invalidate"}))
		return rollback
	}), rollback)
	q, cancel := database.WithContext(ctx)
	var count int64
	require.NoError(t, q.Table("outbox_event").Count(&count).Error)
	cancel()
	require.Zero(t, count)
	r := miniredis.RunT(t)
	cfg := config.Redis{Addr: r.Addr(), PoolSize: 10}
	shared, err := cache.Open(ctx, cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, shared.Close()) }()
	require.NoError(t, shared.Set(ctx, authz.VersionKey(1, 7), "41", 0))
	p := worker.NewQueuePublisher(cfg)
	stale, err := authz.New(shared).Load(ctx, 1, 7, "perm", func(context.Context) ([]byte, error) { return []byte(`["shop:store:delete"]`), nil })
	require.NoError(t, err)
	require.JSONEq(t, `["shop:store:delete"]`, string(stale))
	defer func() { require.NoError(t, p.Close()) }()
	d, err := outbox.NewDispatcher(database, p, nil)
	require.NoError(t, err)
	id := emitAuthorization(ctx, t, database)
	r.SetError("injected Redis failure")
	require.NoError(t, d.RunOnce(ctx))
	state, attempts := eventState(ctx, t, database, id)
	require.Equal(t, "failed", state)
	require.Equal(t, 1, attempts)
	r.SetError("")
	require.NoError(t, database.Exec(ctx, "UPDATE outbox_event SET available_at = now() - interval '1 second' WHERE id = ?", id))
	captured := &capturedPublisher{}
	d, err = outbox.NewDispatcher(database, captured, nil)
	require.NoError(t, err)
	require.NoError(t, d.RunOnce(ctx))
	require.Len(t, captured.events, 1)
	event := captured.events[0]
	consumer, err := outbox.NewConsumer(d, shared, authorizationEventHandler(shared))
	require.NoError(t, err)
	require.NoError(t, consumer.Handle(ctx, id, event.Attempts))
	// A different process has no memory of the event but still skips a sent event.
	restarted, err := outbox.NewConsumer(d, shared, authorizationEventHandler(shared))
	require.NoError(t, err)
	require.NoError(t, restarted.Handle(ctx, id, event.Attempts))
	version, _, err := shared.Get(ctx, authz.VersionKey(1, 7))
	require.NoError(t, err)
	require.Equal(t, "42", version)
	// Repeated external invalidations can only advance the version, never grant.
	granted, err := authz.New(shared).Load(ctx, 1, 7, "perm", func(context.Context) ([]byte, error) { return []byte(`[]`), nil })
	require.NoError(t, err)
	require.JSONEq(t, `[]`, string(granted))
	require.NoError(t, authorizationEventHandler(shared)(ctx, event))
	version, _, err = shared.Get(ctx, authz.VersionKey(1, 7))
	require.NoError(t, err)
	require.Equal(t, "43", version)
	granted, err = authz.New(shared).Load(ctx, 1, 7, "perm", func(context.Context) ([]byte, error) { return []byte(`[]`), nil })
	require.NoError(t, err)
	require.JSONEq(t, `[]`, string(granted))
}

func TestOutboxIntegrationInterruptedAttemptLeaseAndFencing(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	r := miniredis.RunT(t)
	shared, err := cache.Open(ctx, config.Redis{Addr: r.Addr(), PoolSize: 10})
	require.NoError(t, err)
	defer func() { require.NoError(t, shared.Close()) }()
	p := &capturedPublisher{}
	d, err := outbox.NewDispatcher(database, p, nil)
	require.NoError(t, err)
	id := emitAuthorization(ctx, t, database)
	require.NoError(t, d.RunOnce(ctx))
	workCtx, interrupt := context.WithCancel(ctx)
	consumer, err := outbox.NewConsumer(d, shared, func(context.Context, outbox.Event) error { interrupt(); return nil })
	require.NoError(t, err)
	require.ErrorIs(t, consumer.Handle(workCtx, id, 1), context.Canceled)
	state, _ := eventState(ctx, t, database, id)
	require.Equal(t, "processing", state)
	require.NoError(t, database.Exec(ctx, "UPDATE outbox_event SET available_at = now() - interval '1 second' WHERE id = ?", id))
	require.NoError(t, d.RunOnce(ctx))
	var calls int
	consumer, err = outbox.NewConsumer(d, shared, func(context.Context, outbox.Event) error { calls++; return nil })
	require.NoError(t, err)
	require.NoError(t, consumer.Handle(ctx, id, 1)) // Stale attempt is fenced.
	require.Zero(t, calls)
	require.NoError(t, consumer.Handle(ctx, id, 2))
	require.Equal(t, 1, calls)
	state, attempts := eventState(ctx, t, database, id)
	require.Equal(t, "sent", state)
	require.Equal(t, 2, attempts)
	require.NotContains(t, strings.ToLower(state), "processing")
}

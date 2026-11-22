package worker

import (
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/stretchr/testify/require"
)

func TestThumbnailQueueIdentifiersTimeoutAndNoRetry(t *testing.T) {
	r, _ := testQueueConfig(t)
	log, err := logger.New(io.Discard, "info", "json")
	require.NoError(t, err)
	var called bool
	server, err := NewThumbnailQueueServer(r, log, func(ctx context.Context, tenant, id int64) error {
		called = true
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 8, id)
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.WithinDuration(t, time.Now().Add(60*time.Second), deadline, time.Second)
		return nil
	})
	require.NoError(t, err)
	for _, raw := range []string{`{}`, `{"tenant_id":1,"file_id":0}`, `{"tenant_id":1,"file_id":1,"key":"foreign"}`, `{"tenant_id":1,"file_id":1} {}`} {
		require.ErrorIs(t, server.mux.ProcessTask(context.Background(), asynq.NewTask(ThumbnailTask, []byte(raw))), asynq.SkipRetry)
	}
	require.False(t, called)
	require.NoError(t, server.mux.ProcessTask(context.Background(), asynq.NewTask(ThumbnailTask, []byte(`{"tenant_id":7,"file_id":8}`))))
	require.True(t, called)
	publisher := NewQueuePublisher(r)
	defer func() { require.NoError(t, publisher.Close()) }()
	payload := ThumbnailPayload{TenantID: 7, FileID: 8}
	require.NoError(t, publisher.EnqueueThumbnail(context.Background(), payload))
	require.NoError(t, publisher.EnqueueThumbnail(context.Background(), payload))
	inspector := asynq.NewInspector(RedisOptions(r))
	defer func() { require.NoError(t, inspector.Close()) }()
	tasks, err := inspector.ListPendingTasks("thumb")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, 60*time.Second, tasks[0].Timeout)
	require.Zero(t, tasks[0].MaxRetry)
	var actual ThumbnailPayload
	require.NoError(t, json.Unmarshal(tasks[0].Payload, &actual))
	require.Equal(t, payload, actual)
}

func TestThumbnailQueueFiveTasksUseTwoSlots(t *testing.T) {
	// BE-IDN-028 §4.7: deterministic gate, without timing/capacity assertions in CI.
	r, _ := testQueueConfig(t)
	log, err := logger.New(io.Discard, "info", "json")
	require.NoError(t, err)
	started := make(chan int64, 5)
	release := make(chan struct{})
	var active, peak, completed atomic.Int64
	server, err := NewThumbnailQueueServer(r, log, func(ctx context.Context, _, id int64) error {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- id
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			completed.Add(1)
			return nil
		}
	})
	require.NoError(t, err)
	publisher := NewQueuePublisher(r)
	defer func() { require.NoError(t, publisher.Close()) }()
	for id := int64(1); id <= 5; id++ {
		require.NoError(t, publisher.EnqueueThumbnail(context.Background(), ThumbnailPayload{TenantID: 1, FileID: id}))
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("reserved slots did not start")
		}
	}
	require.EqualValues(t, 2, active.Load())
	inspector := asynq.NewInspector(RedisOptions(r))
	defer func() { require.NoError(t, inspector.Close()) }()
	info, err := inspector.GetQueueInfo("thumb")
	require.NoError(t, err)
	require.Equal(t, 2, info.Active)
	require.Equal(t, 3, info.Pending)
	close(release)
	require.Eventually(t, func() bool { return completed.Load() == 5 }, 5*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 2, peak.Load())
}

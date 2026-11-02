package worker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type testSource struct {
	closed chan struct{}
}

func (s *testSource) Receive(ctx context.Context) (TaskPayload, error) {
	<-ctx.Done()
	return TaskPayload{}, ctx.Err()
}

func (s *testSource) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

type gracefulSource struct {
	closed chan struct{}
	sent   bool
}

func (s *gracefulSource) Receive(ctx context.Context) (TaskPayload, error) {
	if !s.sent {
		s.sent = true
		return TaskPayload{TenantID: 1, JobID: 2, Trigger: "cron"}, nil
	}
	<-ctx.Done()
	return TaskPayload{}, ctx.Err()
}

func (s *gracefulSource) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

func TestDecodeTaskPayloadRejectsWorkerCapabilities(t *testing.T) {
	_, err := DecodeTaskPayload([]byte(`{"tenant_id":1,"job_id":2,"run_id":-1,"trigger":"cron"}`))
	require.Error(t, err)
	_, err = DecodeTaskPayload([]byte(`{"tenant_id":1,"job_id":2,"run_id":0,"trigger":"cron","scope":{}}`))
	require.Error(t, err)
}

func TestServerShutdownClosesSource(t *testing.T) {
	source := &testSource{closed: make(chan struct{})}
	server, err := NewServerWithSource(func(context.Context, TaskPayload) error { return nil }, source)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("worker did not shut down")
	}
	select {
	case <-source.closed:
	case <-time.After(time.Second):
		t.Fatal("worker source was not closed")
	}
}

func TestServerShutdownAllowsInFlightTaskToFinish(t *testing.T) {
	source := &gracefulSource{closed: make(chan struct{})}
	started := make(chan struct{})
	release := make(chan struct{})
	server, err := NewServerWithSource(func(ctx context.Context, _ TaskPayload) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			t.Errorf("in-flight task was cancelled during graceful shutdown")
			return ctx.Err()
		}
	}, source)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	<-started
	cancel()
	select {
	case <-source.closed:
	case <-time.After(time.Second):
		t.Fatal("worker source was not closed")
	}
	select {
	case err := <-done:
		t.Fatalf("worker exited before in-flight task completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("worker did not finish after in-flight task completed")
	}
}

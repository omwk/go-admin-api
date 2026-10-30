package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/stretchr/testify/require"
)

type fakeExecutor struct {
	query string
	args  []any
	err   error
}

func (f *fakeExecutor) Exec(_ context.Context, query string, args ...any) error {
	f.query, f.args = query, args
	return f.err
}

func TestRecordAppendsValidatedEvent(t *testing.T) {
	ctx := logger.WithTraceID(context.Background(), "trace-for-test")
	executor := &fakeExecutor{}
	event := Event{
		TenantID: 1, ActorType: "system", Action: "identity.session.revoke",
		ResourceType: "session", ResourceID: "session-id", Result: "success",
	}
	require.NoError(t, Record(ctx, executor, event))
	require.Contains(t, executor.query, "INSERT INTO audit_log")
	require.Equal(t, 12, len(executor.args))
	require.Equal(t, []byte("{}"), executor.args[6])
	require.Equal(t, []byte("{}"), executor.args[7])
	require.Nil(t, executor.args[8])
	require.Equal(t, "trace-for-test", executor.args[10])
}

func TestRecordRejectsInvalidEventAndReturnsWriteFailure(t *testing.T) {
	ctx := logger.WithTraceID(context.Background(), "trace-for-test")
	executor := &fakeExecutor{}
	require.Error(t, Record(ctx, executor, Event{}))
	require.Empty(t, executor.query)

	failure := errors.New("database unavailable")
	executor.err = failure
	err := Record(ctx, executor, Event{
		TenantID: 1, ActorType: "system", Action: "identity.session.revoke",
		ResourceType: "session", ResourceID: "session-id", Result: "success",
	})
	require.ErrorIs(t, err, failure)
	require.Contains(t, err.Error(), "write audit record")
}

func TestRecordRejectsMalformedIP(t *testing.T) {
	ctx := logger.WithTraceID(context.Background(), "trace-for-test")
	err := Record(ctx, &fakeExecutor{}, Event{
		TenantID: 1, ActorType: "system", Action: "identity.session.revoke",
		ResourceType: "session", ResourceID: "session-id", IP: "not-an-ip", Result: "success",
	})
	require.ErrorContains(t, err, "ip")
}

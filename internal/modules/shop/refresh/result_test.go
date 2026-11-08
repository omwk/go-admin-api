package refresh

import (
	"context"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
	"github.com/stretchr/testify/require"
)

func TestExecutorRejectsIncompleteResultsWithoutReconsuming(t *testing.T) {
	for _, bundle := range []string{`{}`, `[]`, `null`, `{"value":null}`, `{" ":true}`, `{"value":`} {
		t.Run(bundle, func(t *testing.T) {
			f := newExecutorFixture(t)
			ctx := context.Background()
			f.platform.handle = func(context.Context, port.Request) (port.Result, error) {
				return port.Result{Values: []byte(bundle), ExpiresAt: f.now.Add(time.Hour), RefreshAt: f.now.Add(30 * time.Minute)}, nil
			}
			run, err := f.executor.Prepare(ctx, f.target)
			require.NoError(t, err)
			run, err = f.executor.Execute(ctx, f.target, run.ID)
			require.ErrorIs(t, err, ErrResultUnknown)
			require.Equal(t, PhaseUnknown, run.Phase)
			require.Equal(t, 5, f.store.snap.Credential.RetryBudget)
			require.Len(t, f.store.versions, 1)
			_, err = f.executor.Execute(ctx, f.target, run.ID)
			require.NoError(t, err)
			require.Equal(t, int32(1), f.platform.calls.Load())
		})
	}
}

func TestExecutorSuccessfulShortTTLResetsOnlyItsTemporaryFailures(t *testing.T) {
	f := newExecutorFixture(t)
	ctx := context.Background()
	f.store.snap.Credential.ConsecutiveFailures = 2
	f.store.snap.Credential.RetryBudget = 3
	var input, output []byte
	f.platform.handle = func(_ context.Context, r port.Request) (port.Result, error) {
		input = r.Values
		output = append([]byte(nil), f.output...)
		return port.Result{Values: output, ExpiresAt: f.now.Add(time.Hour), RefreshAt: f.now.Add(30 * time.Minute)}, nil
	}
	run, err := f.executor.Prepare(ctx, f.target)
	require.NoError(t, err)
	run, err = f.executor.Execute(ctx, f.target, run.ID)
	require.NoError(t, err)
	require.Equal(t, PhasePublished, run.Phase)
	require.Equal(t, "expiring", f.store.snap.Credential.Status)
	require.Zero(t, f.store.snap.Credential.ConsecutiveFailures)
	require.Equal(t, 5, f.store.snap.Credential.RetryBudget)
	require.Equal(t, make([]byte, len(input)), input)
	require.Equal(t, make([]byte, len(output)), output)
}

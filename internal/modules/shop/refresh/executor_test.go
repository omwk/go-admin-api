package refresh

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
	"github.com/stretchr/testify/require"
)

func TestExecutorCrashRecoveryAtEveryPhase(t *testing.T) {
	// REQ-SHP-008: crash after durable commit, discard executor, then recover from storage.
	for _, phase := range []string{PhaseIntent, PhaseSending, PhaseConsumed, PhasePublished, PhaseUnknown} {
		t.Run(phase, func(t *testing.T) {
			f := newExecutorFixture(t)
			ctx := context.Background()
			if phase == PhaseUnknown {
				f.platform.handle = func(context.Context, port.Request) (port.Result, error) {
					return port.Result{}, &port.Failure{Timeout: true}
				}
			}
			var run model.RefreshRun
			if phase == PhaseIntent {
				f.store.crashAt = phase
				require.PanicsWithValue(t, "simulated process exit", func() { _, err := f.executor.Prepare(ctx, f.target); require.NoError(t, err) })
				run = f.store.runs[1]
			} else {
				var err error
				run, err = f.executor.Prepare(ctx, f.target)
				require.NoError(t, err)
				f.store.crashAt = phase
				require.PanicsWithValue(t, "simulated process exit", func() { _, _ = f.executor.Execute(ctx, f.target, run.ID) })
			}
			require.Equal(t, phase, f.store.runs[run.ID].Phase)
			restarted, err := New(f.store, f.executor.sealer, f.platform, f.executor.config, func() time.Time { return f.now })
			require.NoError(t, err)
			f.now = f.now.Add(time.Minute)
			got, err := restarted.Recover(ctx, f.target, run.ID)
			require.NoError(t, err)
			want, calls := PhasePublished, int32(1)
			if phase == PhaseUnknown {
				want = PhaseUnknown
			}
			if phase == PhaseSending {
				want, calls = PhaseUnknown, 0
			}
			require.Equal(t, want, got.Phase)
			require.Equal(t, calls, f.platform.calls.Load())
			_, err = restarted.Execute(ctx, f.target, run.ID)
			require.NoError(t, err)
			require.Equal(t, calls, f.platform.calls.Load())
		})
	}
}

func TestExecutorReturnedResultWithFailedStorageNeverConsumesAgain(t *testing.T) {
	f := newExecutorFixture(t)
	ctx := context.Background()
	run, err := f.executor.Prepare(ctx, f.target)
	require.NoError(t, err)
	f.store.failStage = true
	run, err = f.executor.Execute(ctx, f.target, run.ID)
	require.ErrorIs(t, err, ErrResultUnknown)
	require.Equal(t, PhaseUnknown, run.Phase)
	require.Equal(t, int32(1), f.platform.calls.Load())
	require.Len(t, f.store.reconciliations, 1)
	require.Zero(t, f.store.snap.Credential.ConsecutiveFailures)
	require.Equal(t, 5, f.store.snap.Credential.RetryBudget)
	f.store.failStage = false
	f.now = f.now.Add(24 * time.Hour)
	run, err = f.executor.Prepare(ctx, f.target)
	require.NoError(t, err)
	_, err = f.executor.Recover(ctx, f.target, run.ID)
	require.NoError(t, err)
	_, err = f.executor.Execute(ctx, f.target, run.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), f.platform.calls.Load())
	require.Len(t, f.store.versions, 1)
}

func TestExecutorTemporaryFailuresStopAtFiveAcrossRestarts(t *testing.T) {
	f := newExecutorFixture(t)
	ctx := context.Background()
	f.platform.handle = func(context.Context, port.Request) (port.Result, error) {
		return port.Result{}, &port.Failure{HTTPStatus: 429, NotConsumed: true}
	}
	for attempt := 1; attempt <= 5; attempt++ {
		run, err := f.executor.Prepare(ctx, f.target)
		require.NoError(t, err)
		require.Equal(t, attempt, run.Attempt)
		run, err = f.executor.Execute(ctx, f.target, run.ID)
		require.NoError(t, err)
		require.Equal(t, attempt, f.store.snap.Credential.ConsecutiveFailures)
		require.Equal(t, 5-attempt, f.store.snap.Credential.RetryBudget)
		require.NotEqual(t, "needs_reauth", f.store.snap.Credential.Status)
		duplicate, err := f.executor.Prepare(ctx, f.target)
		require.NoError(t, err)
		require.Equal(t, run.ID, duplicate.ID)
		_, err = f.executor.Execute(ctx, f.target, duplicate.ID)
		require.NoError(t, err)
		require.EqualValues(t, attempt, f.platform.calls.Load())
		f.now = *run.NextRetryAt
		f.executor, err = New(f.store, f.executor.sealer, f.platform, f.executor.config, func() time.Time { return f.now })
		require.NoError(t, err)
	}
	run, err := f.executor.Prepare(ctx, f.target)
	require.NoError(t, err)
	_, err = f.executor.Execute(ctx, f.target, run.ID)
	require.NoError(t, err)
	require.Equal(t, int32(5), f.platform.calls.Load())
	require.Len(t, f.store.runs, 5)
}

func TestExecutorRevocationHasIndependentCounters(t *testing.T) {
	f := newExecutorFixture(t)
	ctx := context.Background()
	f.store.snap.Credential.ConsecutiveFailures = 2
	f.store.snap.Credential.RetryBudget = 3
	f.platform.handle = func(context.Context, port.Request) (port.Result, error) {
		return port.Result{}, &port.Failure{Revoked: true}
	}
	run, err := f.executor.Prepare(ctx, f.target)
	require.NoError(t, err)
	run, err = f.executor.Execute(ctx, f.target, run.ID)
	require.NoError(t, err)
	require.Equal(t, FailureInvalid, run.FailureClass)
	require.Equal(t, "needs_reauth", f.store.snap.Credential.Status)
	require.Equal(t, 2, f.store.snap.Credential.ConsecutiveFailures)
	require.Equal(t, 3, f.store.snap.Credential.RetryBudget)
	_, err = f.executor.Execute(ctx, f.target, run.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), f.platform.calls.Load())
}

func TestExecutorConcurrentTriggersDispatchExactlyOnce(t *testing.T) {
	f := newExecutorFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	handler := f.platform.handle
	f.platform.handle = func(ctx context.Context, r port.Request) (port.Result, error) {
		close(entered)
		select {
		case <-release:
			return handler(ctx, r)
		case <-ctx.Done():
			return port.Result{}, ctx.Err()
		}
	}
	start := make(chan struct{})
	results := make(chan error, 10)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			run, err := f.executor.Prepare(ctx, f.target)
			if err == nil {
				_, err = f.executor.Execute(ctx, f.target, run.ID)
			}
			results <- err
		}()
	}
	close(start)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("dispatch not started")
	}
	for i := 0; i < 9; i++ {
		select {
		case err := <-results:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal("duplicate trigger blocked")
		}
	}
	close(release)
	wg.Wait()
	require.NoError(t, <-results)
	require.Equal(t, int32(1), f.platform.calls.Load())
	require.Len(t, f.store.versions, 2)
	run, err := f.executor.Prepare(ctx, f.target)
	require.NoError(t, err)
	_, err = f.executor.Execute(ctx, f.target, run.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), f.platform.calls.Load())
}

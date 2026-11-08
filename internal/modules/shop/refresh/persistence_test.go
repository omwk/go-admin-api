package refresh

import (
	"context"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
	"github.com/stretchr/testify/require"
)

func TestExecutorPublicationAndCommitAcknowledgementRecovery(t *testing.T) {
	for _, phase := range []string{PhaseConsumed, PhasePublished, "publish_rollback"} {
		t.Run(phase, func(t *testing.T) {
			f := newExecutorFixture(t)
			ctx := context.Background()
			run, err := f.executor.Prepare(ctx, f.target)
			require.NoError(t, err)
			if phase == "publish_rollback" {
				f.store.failPublish = true
			} else {
				f.store.loseCommit = phase
			}
			run, err = f.executor.Execute(ctx, f.target, run.ID)
			if phase == PhaseConsumed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			f.store.failPublish = false
			run, err = f.executor.Recover(ctx, f.target, run.ID)
			require.NoError(t, err)
			require.Equal(t, PhasePublished, run.Phase)
			require.Equal(t, int32(1), f.platform.calls.Load())
			require.Len(t, f.store.versions, 2)
			old := f.store.versions[1]
			require.Equal(t, old.RetiredAt.Add(90*24*time.Hour), *old.PurgeAfter)
			require.Empty(t, f.store.stages[*run.StagedResultID].CipherText)
			require.NotEmpty(t, f.store.versions[*run.OutputVersionID].CipherText)
		})
	}
}

func TestExecutorSendingBarrierFailureMakesNoPlatformCall(t *testing.T) {
	f := newExecutorFixture(t)
	ctx := context.Background()
	run, err := f.executor.Prepare(ctx, f.target)
	require.NoError(t, err)
	f.store.failSending = true
	_, err = f.executor.Execute(ctx, f.target, run.ID)
	require.Error(t, err)
	require.Zero(t, f.platform.calls.Load())
	require.Equal(t, PhaseIntent, f.store.runs[run.ID].Phase)
	f.store.failSending = false
	_, err = f.executor.Recover(ctx, f.target, run.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), f.platform.calls.Load())
}

func TestExecutorReadFailureDuringStagingKeepsOriginalRunIdentity(t *testing.T) {
	f := newExecutorFixture(t)
	ctx := context.Background()
	handler := f.platform.handle
	f.platform.handle = func(ctx context.Context, r port.Request) (port.Result, error) {
		f.store.failReads = 2
		return handler(ctx, r)
	}
	run, err := f.executor.Prepare(ctx, f.target)
	require.NoError(t, err)
	got, err := f.executor.Execute(ctx, f.target, run.ID)
	require.ErrorIs(t, err, ErrResultUnknown)
	require.Equal(t, run.ID, got.ID)
	require.Equal(t, PhaseUnknown, got.Phase)
	require.Len(t, f.store.reconciliations, 1)
	require.Equal(t, int32(1), f.platform.calls.Load())
}

func TestExecutorLateFailureCannotReopenUnknownDispatch(t *testing.T) {
	f := newExecutorFixture(t)
	ctx := context.Background()
	run, err := f.executor.Prepare(ctx, f.target)
	require.NoError(t, err)
	f.platform.handle = func(context.Context, port.Request) (port.Result, error) {
		f.now = f.now.Add(time.Minute)
		recovered, err := f.executor.Recover(ctx, f.target, run.ID)
		require.NoError(t, err)
		require.Equal(t, PhaseUnknown, recovered.Phase)
		return port.Result{}, &port.Failure{HTTPStatus: 429, NotConsumed: true}
	}
	run, err = f.executor.Execute(ctx, f.target, run.ID)
	require.NoError(t, err)
	require.Equal(t, PhaseUnknown, run.Phase)
	require.Zero(t, f.store.snap.Credential.ConsecutiveFailures)
	require.Equal(t, 5, f.store.snap.Credential.RetryBudget)
	f.now = f.now.Add(24 * time.Hour)
	duplicate, err := f.executor.Prepare(ctx, f.target)
	require.NoError(t, err)
	_, err = f.executor.Execute(ctx, f.target, duplicate.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), f.platform.calls.Load())
}

func TestExecutorLateResultCannotOverwriteReplacement(t *testing.T) {
	f := newExecutorFixture(t)
	ctx := context.Background()
	handler := f.platform.handle
	f.platform.handle = func(ctx context.Context, r port.Request) (port.Result, error) {
		f.store.mu.Lock()
		replacement := int64(9)
		f.store.snap.Credential.RefreshGeneration++
		f.store.snap.Credential.CurrentVersionID = &replacement
		f.store.mu.Unlock()
		return handler(ctx, r)
	}
	run, err := f.executor.Prepare(ctx, f.target)
	require.NoError(t, err)
	run, err = f.executor.Execute(ctx, f.target, run.ID)
	require.NoError(t, err)
	require.Equal(t, "skipped", run.Status)
	require.EqualValues(t, 9, *f.store.snap.Credential.CurrentVersionID)
	require.Len(t, f.store.versions, 1)
	require.Equal(t, int32(1), f.platform.calls.Load())
}

func TestExecutorExpiredOrTamperedStagingDoesNotResend(t *testing.T) {
	for _, mode := range []string{"expired", "tampered"} {
		t.Run(mode, func(t *testing.T) {
			f := newExecutorFixture(t)
			ctx := context.Background()
			run, err := f.executor.Prepare(ctx, f.target)
			require.NoError(t, err)
			f.store.failPublish = true
			run, err = f.executor.Execute(ctx, f.target, run.ID)
			require.Error(t, err)
			f.store.failPublish = false
			if mode == "expired" {
				f.now = f.now.Add(24 * time.Hour)
			} else {
				stage := f.store.stages[*run.StagedResultID]
				stage.CipherText = append([]byte(nil), stage.CipherText...)
				stage.CipherText[0] ^= 1
				f.store.stages[stage.ID] = stage
			}
			run, err = f.executor.Recover(ctx, f.target, run.ID)
			if mode == "expired" {
				require.NoError(t, err)
				require.Equal(t, PhaseUnknown, run.Phase)
				require.Len(t, f.store.reconciliations, 1)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, int32(1), f.platform.calls.Load())
			require.Len(t, f.store.versions, 1)
		})
	}
}

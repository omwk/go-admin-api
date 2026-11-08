package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
	"github.com/stretchr/testify/require"
)

func TestCredentialRefreshIntegrationCrashRecoveryFivePhases(t *testing.T) {
	for _, phase := range []string{refresh.PhaseIntent, refresh.PhaseSending, refresh.PhaseConsumed, refresh.PhasePublished, refresh.PhaseUnknown} {
		t.Run(phase, func(t *testing.T) {
			f := newCredentialRefreshFixture(t)
			faults := &credentialRefreshFaults{Store: f.refreshStore, crashAt: phase}
			f.executor = f.newExecutor(t, faults)
			if phase == refresh.PhaseUnknown {
				f.platform.handle = func(context.Context, port.Request) (port.Result, error) {
					return port.Result{}, &port.Failure{Timeout: true}
				}
			}
			if phase == refresh.PhaseIntent {
				require.PanicsWithValue(t, "simulated process exit", func() { _, _ = f.executor.Prepare(f.ctx, f.target) })
			} else {
				run := f.prepare(t)
				require.PanicsWithValue(t, "simulated process exit", func() { _, _ = f.executor.Execute(f.ctx, f.target, run.ID) })
			}
			stored := f.storedRun(t)
			require.Equal(t, phase, stored.Phase)
			f.now = f.now.Add(time.Minute)
			restarted := f.newExecutor(t, &credentialRefreshStore{database: f.database})
			run, err := restarted.Recover(f.ctx, f.target, stored.ID)
			require.NoError(t, err)
			want, calls := refresh.PhasePublished, int32(1)
			if phase == refresh.PhaseSending {
				want, calls = refresh.PhaseUnknown, 0
			}
			if phase == refresh.PhaseUnknown {
				want = refresh.PhaseUnknown
			}
			require.Equal(t, want, run.Phase)
			require.Equal(t, calls, f.platform.calls.Load())
			_, err = restarted.Execute(f.ctx, f.target, run.ID)
			require.NoError(t, err)
			require.Equal(t, calls, f.platform.calls.Load())
			f.assertSafe(t)
		})
	}
}

func TestCredentialRefreshIntegrationReturnedResultStorageFailureIsUnknown(t *testing.T) {
	f := newCredentialRefreshFixture(t)
	faults := &credentialRefreshFaults{Store: f.refreshStore, failStage: true}
	f.executor = f.newExecutor(t, faults)
	run := f.prepare(t)
	run, err := f.executor.Execute(f.ctx, f.target, run.ID)
	require.ErrorIs(t, err, refresh.ErrResultUnknown)
	f.noLeaks(t, "refresh error", err.Error())
	require.Equal(t, refresh.PhaseUnknown, run.Phase)
	f.executor = f.newExecutor(t, f.refreshStore)
	f.now = f.now.Add(24 * time.Hour)
	for i := 0; i < 3; i++ {
		run = f.prepare(t)
		_, err = f.executor.Recover(f.ctx, f.target, run.ID)
		require.NoError(t, err)
		_, err = f.executor.Execute(f.ctx, f.target, run.ID)
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), f.platform.calls.Load())
	row, _ := f.readStored(t, f.target.CredentialID, 1)
	require.Zero(t, row.ConsecutiveFailures)
	require.Equal(t, 5, row.RetryBudget)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("credential_refresh_reconciliation").Where("resolved_at IS NULL").Count(&count).Error)
	require.EqualValues(t, 1, count)
	f.assertSafe(t)
}

func TestCredentialRefreshIntegrationPublicationRollbackResumesLocally(t *testing.T) {
	f := newCredentialRefreshFixture(t)
	f.executor = f.newExecutor(t, &credentialRefreshFaults{Store: f.refreshStore, failPublish: true})
	run := f.prepare(t)
	run, err := f.executor.Execute(f.ctx, f.target, run.ID)
	require.Error(t, err)
	require.Equal(t, refresh.PhaseConsumed, run.Phase)
	c, v1 := f.readStored(t, f.target.CredentialID, 1)
	require.Equal(t, v1.ID, *c.CurrentVersionID)
	require.Nil(t, v1.RetiredAt)
	f.executor = f.newExecutor(t, f.refreshStore)
	run, err = f.executor.Recover(f.ctx, f.target, run.ID)
	require.NoError(t, err)
	require.Equal(t, refresh.PhasePublished, run.Phase)
	require.Equal(t, int32(1), f.platform.calls.Load())
	c, v1 = f.readStored(t, f.target.CredentialID, 1)
	_, v2 := f.readStored(t, f.target.CredentialID, 2)
	require.Equal(t, v2.ID, *c.CurrentVersionID)
	require.Equal(t, v1.RetiredAt.Add(90*24*time.Hour), *v1.PurgeAfter)
	plain, err := f.sealer.Open(v2.CipherText, v2.Nonce, v2.AAD, v2.KeyVersion)
	require.NoError(t, err)
	defer clear(plain)
	require.True(t, string(plain) == string(f.output), "published result differs")
	f.assertSafe(t)
}

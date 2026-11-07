package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGrantIntegrationFairnessSurvivesRestart(t *testing.T) {
	// REQ-SYNC-011 / ADR-018: continuous sync cannot starve replay.
	f := replayFixture(t, 1)
	replay := createReplay(t, f)
	run := f.run(t)
	now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Second)
	for n := 1; n <= 10; n++ {
		f.engineStore.now = func() time.Time { return now }
		kind, id := "sync", run.ID
		if n%5 == 0 {
			_, err := f.engineStore.acquireGrant(f.ctx, f.jobID, "sync", run.ID)
			require.ErrorIs(t, err, errGrantBusy)
			kind, id = "replay", replay.ID
		}
		grant, err := f.engineStore.acquireGrant(f.ctx, f.jobID, kind, id)
		require.NoError(t, err)
		require.Equal(t, kind, grant.Kind)
		require.NoError(t, f.engineStore.releaseGrant(f.ctx, grant))
		now = now.Add(time.Second)
		f.rebuild(t)
	}
}

func TestGrantIntegrationReplayBlocksSyncAtMostSevenSeconds(t *testing.T) {
	f := replayFixture(t, 1)
	f.engineStore.settings.ReplayBatchTimeout = 0 // Exercise the production default.
	replay := createReplay(t, f)
	now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Second)
	f.engineStore.now = func() time.Time { return now }
	grant, err := f.engineStore.acquireGrant(f.ctx, f.jobID, "replay", replay.ID)
	require.NoError(t, err)
	require.Equal(t, now.Add(2*time.Second), grant.Deadline)
	run := f.run(t) // Admission persists even while replay owns the grant.
	job, err := f.engineStore.ResolveJob(f.ctx, f.jobID)
	require.NoError(t, err)
	executed := false
	require.NoError(t, f.engineStore.withSyncGrant(f.ctx, job, run, func(context.Context) error {
		executed = true
		return nil
	}))
	require.False(t, executed)
	ready := now
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("outbox_event").Select("max(available_at)").Where("event_type='sync.dispatch'").Scan(&now).Error)
	require.True(t, ready.Add(5*time.Second).Equal(now))
	next, err := f.engineStore.acquireGrant(f.ctx, f.jobID, "sync", run.ID)
	require.NoError(t, err)
	require.LessOrEqual(t, now.Sub(ready), 7*time.Second)
	require.Greater(t, next.Epoch, grant.Epoch)
	require.NoError(t, f.engineStore.releaseGrant(f.ctx, next))
}

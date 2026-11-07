package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func assertNoReplayTargets(t *testing.T, f *syncFixture) {
	t.Helper()
	for _, table := range []string{"listing", "listing_sku", "inventory_snapshot", "product_spu", "product_sku", "dead_letter", "quarantine_record"} {
		rawCount(f.ctx, t, f.database, table, 0)
	}
}

func TestReplayIntegrationOldEpochCannotCommit(t *testing.T) {
	// REQ-SYNC-011: DB epoch, rather than a Redis GET, authorizes every write.
	t.Setenv("CH_SYNC_REPLAY_BATCH_TIMEOUT", "2s")
	f := replayFixture(t, 1)
	run := createReplay(t, f)
	now := time.Now().UTC().Add(time.Second)
	f.engineStore.now = func() time.Time { return now }
	old, err := f.engineStore.acquireGrant(f.ctx, f.jobID, "replay", run.ID)
	require.NoError(t, err)
	now = now.Add(3 * time.Second)
	next, err := f.engineStore.acquireGrant(f.ctx, f.jobID, "replay", run.ID)
	require.NoError(t, err)
	require.Greater(t, next.Epoch, old.Epoch)
	_, err = f.engineStore.processGrantedReplay(context.WithValue(f.ctx, grantKey{}, old), 1, run.ID, 0)
	require.ErrorIs(t, err, errGrantFence)
	assertNoReplayTargets(t, f)
	current, err := f.engineStore.ReadReplay(f.ctx, run.ID)
	require.NoError(t, err)
	require.Zero(t, current.Processed)
	require.JSONEq(t, `{}`, string(current.CheckpointKey))
}

func TestSyncIntegrationOldEpochCannotCommit(t *testing.T) {
	f := replayFixture(t, 0)
	run := f.run(t)
	now := time.Now().UTC().Add(time.Second)
	f.engineStore.now = func() time.Time { return now }
	old, err := f.engineStore.acquireGrant(f.ctx, f.jobID, "sync", run.ID)
	require.NoError(t, err)
	work, err := f.engineStore.LoadWork(f.ctx, f.jobID, run.ID)
	require.NoError(t, err)
	now = now.Add(61 * time.Second)
	_, err = f.engineStore.acquireGrant(f.ctx, f.jobID, "sync", run.ID)
	require.NoError(t, err)
	ctx := scope.WithWorkAccess(context.WithValue(f.ctx, grantKey{}, old), work.Access)
	_, err = f.engineStore.CommitPage(ctx, repo.Page{Run: work.Run})
	require.ErrorIs(t, err, errGrantFence)
	assertNoReplayTargets(t, f)
	rawCount(f.ctx, t, f.database, "sync_cursor", 0)
}

type delayedReplayHandler struct {
	syncmodule.ResourceHandler
	after func()
}

func (h delayedReplayHandler) Upsert(ctx context.Context, q *gorm.DB, m ingestion.Mapped) (ingestion.UpsertStats, error) {
	stats, err := h.ResourceHandler.Upsert(ctx, q, m)
	h.after()
	return stats, err
}

func TestReplayIntegrationDeadlineRollsBackWrittenTargets(t *testing.T) {
	t.Setenv("CH_SYNC_REPLAY_BATCH_TIMEOUT", "2s")
	f := replayFixture(t, 1)
	run := createReplay(t, f)
	now := time.Now().UTC().Add(time.Second)
	f.engineStore.now = func() time.Time { return now }
	f.engineStore.handlers["product"] = delayedReplayHandler{ResourceHandler: f.engineStore.handlers["product"], after: func() { now = now.Add(3 * time.Second) }}
	_, err := f.engineStore.ProcessReplay(f.ctx, 1, run.ID, 0)
	require.ErrorIs(t, err, errGrantFence)
	assertNoReplayTargets(t, f)
	persisted, err := f.engineStore.ReadReplay(f.ctx, run.ID)
	require.NoError(t, err)
	require.Zero(t, persisted.Processed)
	t.Run("real_context_deadline", func(t *testing.T) {
		slow, id := slowReplayFixture(t)
		started := time.Now()
		_, err := slow.engineStore.ProcessReplay(slow.ctx, 1, id, 0)
		t.Logf("D1 fixed-clock slow batch elapsed=%s error=%v", time.Since(started), err)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assertNoReplayTargets(t, slow)
		persisted, err := slow.engineStore.ReadReplay(slow.ctx, id)
		require.NoError(t, err)
		require.Zero(t, persisted.Processed)
		require.Nil(t, persisted.FinishedAt)
		require.JSONEq(t, `{}`, string(persisted.CheckpointKey))
	})
	t.Run("configured_context_budget", func(t *testing.T) {
		t.Setenv("CH_SYNC_REPLAY_BATCH_TIMEOUT", "5s")
		slow, id := slowReplayFixture(t)
		started := time.Now()
		_, err := slow.engineStore.ProcessReplay(slow.ctx, 1, id, 0)
		t.Logf("D1 configured slow batch elapsed=%s error=%v", time.Since(started), err)
		require.NoError(t, err)
		persisted, err := slow.engineStore.ReadReplay(slow.ctx, id)
		require.NoError(t, err)
		require.EqualValues(t, 1, persisted.Processed)
		require.Equal(t, "success", persisted.Status)
		require.NotNil(t, persisted.FinishedAt)
		require.NotEqual(t, `{}`, string(persisted.CheckpointKey))
		rawCount(slow.ctx, t, slow.database, "listing", 1)
		q, cancel := slow.database.WithContext(slow.ctx)
		defer cancel()
		grant, err := lockSchedule(q, slow.jobID)
		require.NoError(t, err)
		require.Equal(t, slow.engineStore.now().Add(5*time.Second), grant.Deadline)
	})
	t.Run("configured_fence", func(t *testing.T) {
		t.Setenv("CH_SYNC_REPLAY_BATCH_TIMEOUT", "5s")
		f := replayFixture(t, 1)
		run := createReplay(t, f)
		now := time.Now().UTC().Add(time.Second)
		f.engineStore.now = func() time.Time { return now }
		f.engineStore.handlers["product"] = delayedReplayHandler{ResourceHandler: f.engineStore.handlers["product"], after: func() { now = now.Add(6 * time.Second) }}
		_, err := f.engineStore.ProcessReplay(f.ctx, 1, run.ID, 0)
		require.ErrorIs(t, err, errGrantFence)
		assertNoReplayTargets(t, f)
		persisted, err := f.engineStore.ReadReplay(f.ctx, run.ID)
		require.NoError(t, err)
		require.Zero(t, persisted.Processed)
		require.Nil(t, persisted.FinishedAt)
		require.JSONEq(t, `{}`, string(persisted.CheckpointKey))
	})
}

func slowReplayFixture(t *testing.T) (*syncFixture, int64) {
	t.Helper()
	f := replayFixture(t, 1)
	run := createReplay(t, f)
	stamp := time.Now().UTC().Add(time.Second)
	f.engineStore.now = func() time.Time { return stamp }
	f.engineStore.handlers["product"] = delayedReplayHandler{
		ResourceHandler: f.engineStore.handlers["product"],
		after: func() {
			// D1: one injected slow operation, independent of external load.
			delay := time.NewTimer(2100 * time.Millisecond)
			defer delay.Stop()
			<-delay.C
		},
	}
	return f, run.ID
}

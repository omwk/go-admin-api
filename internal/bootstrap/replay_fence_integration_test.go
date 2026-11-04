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
}

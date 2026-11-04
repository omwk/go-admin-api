package bootstrap

import (
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func TestReplayIntegrationPinCancelAndRecoveryCheckpoint(t *testing.T) {
	f := replayFixture(t, 201)
	run := createReplay(t, f)
	run, err := f.engineStore.ProcessReplay(f.ctx, 1, run.ID, 0)
	require.NoError(t, err)
	require.EqualValues(t, 200, run.Processed)
	checkpoint := string(run.CheckpointKey)
	month, err := ingestion.ParseMonth(time.Now().UTC().Format("2006-01"))
	require.NoError(t, err)
	maintenance := NewRawPartitionRetirer(f.database)
	require.ErrorIs(t, maintenance.Retire(f.ctx, month, false), ingestion.ErrPartitionPinned)
	require.ErrorIs(t, maintenance.Retire(f.ctx, month, true), ingestion.ErrPartitionPinned)
	run, err = f.engineStore.CancelReplay(f.ctx, run.ID, nil)
	require.NoError(t, err)
	require.Equal(t, "cancelled", run.Status)
	require.JSONEq(t, checkpoint, string(run.CheckpointKey))
	run, err = f.engineStore.ProcessReplay(f.ctx, 1, run.ID, 200)
	require.NoError(t, err)
	require.Equal(t, "cancelled", run.Status)
	rawCount(f.ctx, t, f.database, "listing", 200)
	rawCount(f.ctx, t, f.database, "inventory_snapshot", 200)
	rawCount(f.ctx, t, f.database, "replay_run_item", 201)
	rawCount(f.ctx, t, f.database, "raw_partition_pin", 1)
	require.NoError(t, maintenance.Retire(f.ctx, month, false))
	require.NoError(t, maintenance.Retire(f.ctx, month, true))
}

func TestReplayIntegrationSingleActiveAndSuccessReleasesPin(t *testing.T) {
	f := replayFixture(t, 1)
	run := createReplay(t, f)
	_, err := f.engineStore.CreateReplay(f.ctx, replayRequest(f), nil)
	require.Error(t, err)
	run, err = f.engineStore.ProcessReplay(f.ctx, 1, run.ID, 0)
	require.NoError(t, err)
	require.Equal(t, "success", run.Status)
	month, err := ingestion.ParseMonth(time.Now().UTC().Format("2006-01"))
	require.NoError(t, err)
	require.NoError(t, NewRawPartitionRetirer(f.database).Retire(f.ctx, month, true))
}

package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func assertReplayRollback(ctx context.Context, t *testing.T, database *DB) {
	t.Helper()
	q := database.handle.WithContext(ctx)
	var index string
	require.NoError(t, q.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname=current_schema() AND indexname='replay_run_item_pkey'").Scan(&index).Error)
	require.Contains(t, index, "UNIQUE INDEX")
	require.Contains(t, index, "(run_id, fetched_at, id)")
	require.NoError(t, q.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname=current_schema() AND indexname='idx_raw_replay_source'").Scan(&index).Error)
	require.Contains(t, index, "(tenant_id, platform_id, shop_id, resource_type, raw_id, fetched_at)")
	var columns []string
	require.NoError(t, q.Raw("SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='replay_run' AND column_name IN ('total','processed','checkpoint_key','mapping_checksum','mapping_spec','function_version','authorization_generation','scheduling_generation','upper_key') ORDER BY column_name").Scan(&columns).Error)
	require.Equal(t, []string{"authorization_generation", "checkpoint_key", "function_version", "mapping_checksum", "mapping_spec", "processed", "scheduling_generation", "total", "upper_key"}, columns)
	state, err := database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 7}, state)
	logMigrationTableCount(ctx, t, database, "down -> 7", 47)
	var count int64
	require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='replay_run_item'").Scan(&count).Error)
	require.Zero(t, count)
	require.NoError(t, q.Raw("SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname='idx_raw_replay_source'").Scan(&count).Error)
	require.Zero(t, count)
	require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='replay_run' AND column_name IN ?", columns).Scan(&count).Error)
	require.Zero(t, count)
}

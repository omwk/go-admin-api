package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func assertExportRollback(ctx context.Context, t *testing.T, database *DB) {
	t.Helper()
	q := database.handle.WithContext(ctx)
	var definition string
	require.NoError(t, q.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname=current_schema() AND indexname='export_job_row_pkey'").Scan(&definition).Error)
	require.Contains(t, definition, "UNIQUE INDEX")
	require.Contains(t, definition, "(export_id, attempt, ordinal)")
	var columns []string
	require.NoError(t, q.Raw("SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='export_job' AND column_name IN ('attempt','row_total','started_at') ORDER BY column_name").Scan(&columns).Error)
	require.Equal(t, []string{"attempt", "row_total", "started_at"}, columns)
	state, err := database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 8}, state)
	var count int64
	require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='export_job_row'").Scan(&count).Error)
	require.Zero(t, count)
	require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='export_job' AND column_name IN ('attempt','row_total')").Scan(&count).Error)
	require.Zero(t, count)
	require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='export_job' AND column_name='started_at'").Scan(&count).Error)
	require.EqualValues(t, 1, count)
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 9}, state)
	logMigrationTableCount(ctx, t, database, "9 -> 8 -> 9", 50)
	state, err = database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 8}, state)
}

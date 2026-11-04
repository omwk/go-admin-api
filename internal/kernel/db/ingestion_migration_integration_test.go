package db

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func ingestionMigrationTables(ctx context.Context, t *testing.T, database *DB) []string {
	t.Helper()
	var partitions []string
	err := database.handle.WithContext(ctx).Raw(`SELECT 'raw_payload_' || to_char(month, 'YYYYMM')
        FROM generate_series(date_trunc('month', now() AT TIME ZONE 'UTC'),
        date_trunc('month', now() AT TIME ZONE 'UTC') + interval '1 month', interval '1 month') month`).Scan(&partitions).Error
	require.NoError(t, err)
	return append([]string{"raw_payload", "raw_dedup", "schema_baseline", "schema_drift_event",
		"mapping_profile", "mapping_version", "quarantine_record", "listing", "listing_sku",
		"sync_job", "sync_cursor", "sync_run", "sync_error", "dead_letter", "replay_run"}, partitions...)
}

func assertIngestionRollback(ctx context.Context, t *testing.T, database *DB) {
	t.Helper()
	state, err := database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 5}, state)
	logMigrationTableCount(ctx, t, database, "down -> 5", 33)
	var syncTables []string
	err = database.handle.WithContext(ctx).Raw(`SELECT table_name FROM information_schema.tables
        WHERE table_schema = current_schema() AND table_name IN
        ('sync_job','sync_cursor','sync_run','sync_error','dead_letter','replay_run')`).Scan(&syncTables).Error
	require.NoError(t, err)
	require.Empty(t, syncTables)
	state, err = database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 4}, state)
	logMigrationTableCount(ctx, t, database, "down -> 4", 22)
	var count int64
	err = database.handle.WithContext(ctx).Raw(`SELECT count(*) FROM pg_proc p
        JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = current_schema()
        AND p.proname = 'ensure_raw_partition'`).Scan(&count).Error
	require.NoError(t, err)
	require.Zero(t, count)
	err = database.handle.WithContext(ctx).Raw(`SELECT count(*) FROM pg_constraint c
        JOIN pg_namespace n ON n.oid = c.connamespace WHERE n.nspname = current_schema()
        AND c.conname IN ('fk_mapping_profile_platform','fk_mapping_version_profile','fk_mapping_profile_active_version')`).Scan(&count).Error
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestIngestionMigrationIntegrationIndexes(t *testing.T) {
	database, ctx := integrationDB(t)
	_, err := database.Migrate(ctx, "up")
	require.NoError(t, err)
	var indexes []string
	err = database.handle.WithContext(ctx).Raw(`SELECT indexname FROM pg_indexes
        WHERE schemaname = current_schema() AND tablename IN
        ('raw_payload','raw_dedup','schema_baseline','schema_drift_event','mapping_profile','mapping_version',
        'quarantine_record','listing','listing_sku','sync_job','sync_cursor','sync_run','sync_error','dead_letter','replay_run')
        ORDER BY indexname`).Scan(&indexes).Error
	require.NoError(t, err)
	expected := []string{"raw_payload_pkey", "uq_raw_dedup", "idx_raw_lookup", "idx_raw_replay_source", "idx_raw_unmapped", "uq_raw_directory",
		"schema_baseline_pkey", "uq_schema_baseline_active", "schema_drift_event_pkey", "idx_drift_open",
		"mapping_profile_pkey", "mapping_profile_tenant_id_id_key", "uq_mapping_profile",
		"mapping_version_pkey", "mapping_version_tenant_id_profile_id_id_key", "uq_mapping_version",
		"quarantine_record_pkey", "idx_quarantine_open", "uq_quarantine_raw",
		"listing_pkey", "uq_listing_external", "idx_listing_spu", "idx_listing_shop_status", "listing_sku_pkey", "uq_listing_sku",
		"sync_job_pkey", "uq_sync_job", "sync_cursor_pkey", "uq_sync_cursor", "sync_run_pkey", "idx_sync_run", "idx_sync_run_running",
		"sync_error_pkey", "idx_sync_error_run", "dead_letter_pkey", "idx_dead_letter", "replay_run_pkey"}
	sort.Strings(expected)
	require.Equal(t, expected, indexes)
	t.Logf("000005/000006: indexes=%d", len(indexes))
}

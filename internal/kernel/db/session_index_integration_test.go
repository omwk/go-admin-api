package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionRevocationIndexIntegrationUpAndDown(t *testing.T) {
	database, ctx := integrationDB(t)
	state, err := database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 9}, state)
	var definition string
	err = database.handle.WithContext(ctx).Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_refresh_session_revoked'").Scan(&definition).Error
	require.NoError(t, err)
	require.Contains(t, definition, "(tenant_id, session_id, user_id)")
	for _, reason := range []string{"logout", "kick", "replay"} {
		require.Contains(t, definition, reason)
	}
	require.Contains(t, definition, "WHERE")
	t.Log("up -> 9: idx_refresh_session_revoked exists")
	for _, version := range []uint{8, 7, 6, 5, 4} {
		state, err = database.Migrate(ctx, "down")
		require.NoError(t, err)
		require.Equal(t, MigrationVersion{Version: version}, state)
		var unchanged string
		err = database.handle.WithContext(ctx).Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_refresh_session_revoked'").Scan(&unchanged).Error
		require.NoError(t, err)
		require.Equal(t, definition, unchanged)
	}
	state, err = database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 3}, state)
	var afterShopRollback string
	err = database.handle.WithContext(ctx).Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_refresh_session_revoked'").Scan(&afterShopRollback).Error
	require.NoError(t, err)
	require.Equal(t, definition, afterShopRollback)
	t.Log("down -> 3: idx_refresh_session_revoked exists, definition unchanged")
	state, err = database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 2}, state)
	var count int64
	err = database.handle.WithContext(ctx).Raw("SELECT count(*) FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_refresh_session_revoked'").Scan(&count).Error
	require.NoError(t, err)
	require.Zero(t, count)
	t.Log("down -> 2: idx_refresh_session_revoked absent")
}

func TestMigrationBudgetIntegrationCallerContextAndTransactions(t *testing.T) {
	database, ctx := integrationDB(t)
	// S1: DDL must complete independently of the interactive transaction budget.
	database.config.TxnTimeout = time.Nanosecond
	database.config.MigrateTimeout = 5 * time.Minute
	state, err := database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 9}, state)
	pool, err := database.handle.DB()
	require.NoError(t, err)
	conn, err := pool.Conn(ctx)
	require.NoError(t, err)
	cleanupTimeout := database.config.MigrationTimeout()
	defer func() { require.NoError(t, resetMigrationConnection(ctx, conn, cleanupTimeout)) }()
	driver := &migrationContextDriver{ctx: ctx, conn: conn}
	require.NoError(t, driver.Run(strings.NewReader("BEGIN; CREATE TABLE migration_budget_committed (id bigint); COMMIT;")))
	var committed bool
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT to_regclass('migration_budget_committed') IS NOT NULL").Scan(&committed))
	require.True(t, committed)
	require.NoError(t, driver.Run(strings.NewReader("BEGIN; CREATE TABLE migration_budget_rollback (id bigint); ROLLBACK;")))
	var rolledBack bool
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT to_regclass('migration_budget_rollback') IS NULL").Scan(&rolledBack))
	require.True(t, rolledBack)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	driver.ctx = canceled
	require.ErrorIs(t, driver.Run(strings.NewReader("SELECT 1")), context.Canceled)
	database.config.MigrateTimeout = time.Nanosecond
	_, err = database.Migrate(ctx, "version")
	require.Error(t, err)
}

func TestMigrationGateIntegrationWaitHonorsDeadline(t *testing.T) {
	database, ctx := integrationDB(t)
	pool, err := database.handle.DB()
	require.NoError(t, err)
	first, err := pool.Conn(ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, first.Close()) }()
	unlock, err := acquireMigrationGate(ctx, first, database.config.MigrationTimeout())
	require.NoError(t, err)
	defer func() { require.NoError(t, unlock()) }()
	waiter, err := pool.Conn(ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, waiter.Close()) }()
	deadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = acquireMigrationGate(deadline, waiter, database.config.MigrationTimeout())
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

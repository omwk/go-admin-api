package db

import (
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/migrations"
	"github.com/stretchr/testify/require"
)

func latestMigration(t *testing.T) uint {
	t.Helper()
	entries, err := fs.Glob(migrations.Files, "*.up.sql")
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	version, _, ok := strings.Cut(entries[len(entries)-1], "_")
	require.True(t, ok)
	latest, err := strconv.ParseUint(version, 10, 32)
	require.NoError(t, err)
	return uint(latest)
}

func TestMigrateIntegrationReversibleAndRepeatable(t *testing.T) {
	// REQ-SYS-005: one complete rollback validates the entire embedded chain.
	database, ctx := isolatedDB(t, false)
	latest := MigrationVersion{Version: latestMigration(t)}
	for _, command := range []string{"up", "up", "version"} {
		state, err := database.Migrate(ctx, command)
		require.NoError(t, err)
		require.Equal(t, latest, state)
	}
	var code string
	require.NoError(t, database.handle.WithContext(ctx).Raw("SELECT code FROM tenant WHERE id=1").Scan(&code).Error)
	require.Equal(t, "default", code)
	state := latest
	for state.Version > 0 {
		previous := state.Version
		var err error
		state, err = database.Migrate(ctx, "down")
		require.NoError(t, err)
		require.False(t, state.Dirty)
		require.Less(t, state.Version, previous)
	}
	var remaining int64
	require.NoError(t, database.handle.WithContext(ctx).Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name <> 'schema_migrations'").Scan(&remaining).Error)
	require.Zero(t, remaining)
	state, err := database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.Equal(t, latest, state)
}

func TestMigrateIntegrationConcurrentStartup(t *testing.T) {
	database, ctx := isolatedDB(t, false)
	latest := latestMigration(t)
	var workers sync.WaitGroup
	errorsFound := make(chan error, 2)
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			state, err := database.Migrate(ctx, "up")
			if err == nil && (state.Version != latest || state.Dirty) {
				err = fmt.Errorf("unexpected migration version")
			}
			errorsFound <- err
		}()
	}
	workers.Wait()
	close(errorsFound)
	for err := range errorsFound {
		require.NoError(t, err)
	}
}

func TestMigrationBudgetIntegrationCallerContextAndTransactions(t *testing.T) {
	database, ctx := isolatedDB(t, false)
	// S1: DDL must complete independently of the interactive transaction budget.
	database.config.TxnTimeout = time.Nanosecond
	database.config.MigrateTimeout = 5 * time.Minute
	state, err := database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: latestMigration(t)}, state)
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
	database, ctx := isolatedDB(t, false)
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

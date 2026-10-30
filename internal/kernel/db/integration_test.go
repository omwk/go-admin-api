package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/stretchr/testify/require"
)

func integrationDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cfg := config.DB{DSN: dsn, MaxOpen: 5, MaxIdle: 2, ConnMaxLifetime: time.Minute}
	admin, err := Open(ctx, cfg)
	require.NoError(t, err)
	schema := "m0_" + strings.ToLower(ulid.Make().String())
	require.NoError(t, admin.handle.WithContext(ctx).Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		require.NoError(t, admin.handle.WithContext(cleanup).Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		require.NoError(t, admin.Close())
	})
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		cfg.DSN = parsed.String()
	} else {
		cfg.DSN += " search_path=" + schema
	}
	database, err := Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	return database, ctx
}

func TestMigrateIntegrationReversibleAndRepeatable(t *testing.T) {
	// REQ-SYS-005: the embedded initial migration is repeatable and reversible.
	database, ctx := integrationDB(t)
	for _, command := range []string{"up", "up", "version"} {
		state, err := database.Migrate(ctx, command)
		require.NoError(t, err)
		require.Equal(t, MigrationVersion{Version: 1}, state)
	}
	var tables []string
	err := database.handle.WithContext(ctx).Raw(
		"SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_name <> 'schema_migrations' ORDER BY table_name",
	).Scan(&tables).Error
	require.NoError(t, err)
	require.Equal(t, []string{"audit_log", "file_object", "idempotency_key", "notification", "outbox_event", "sys_setting", "tenant"}, tables)
	var code string
	require.NoError(t, database.handle.WithContext(ctx).Raw("SELECT code FROM tenant WHERE id = 1").Scan(&code).Error)
	require.Equal(t, "default", code)
	state, err := database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Zero(t, state.Version)
	var count int64
	err = database.handle.WithContext(ctx).Raw(
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name <> 'schema_migrations'",
	).Scan(&count).Error
	require.NoError(t, err)
	require.Zero(t, count)
	_, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
}

func TestTransactionIntegrationNestedCommitAndRollback(t *testing.T) {
	database, ctx := integrationDB(t)
	_, err := database.Migrate(ctx, "up")
	require.NoError(t, err)
	rollback := errors.New("rollback requested")
	for _, tc := range []struct {
		name  string
		err   error
		count int64
	}{
		{"rollback", rollback, 0},
		{"commit", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := database.Transaction(ctx, func(txCtx context.Context) error {
				tx, cancel := database.WithContext(txCtx)
				defer cancel()
				if err := tx.Exec("INSERT INTO tenant (code, name) VALUES (?, ?)", tc.name, tc.name).Error; err != nil {
					return err
				}
				return database.Transaction(txCtx, func(nestedCtx context.Context) error {
					nested, cancel := database.WithContext(nestedCtx)
					defer cancel()
					require.Same(t, tx.Statement.ConnPool, nested.Statement.ConnPool)
					return tc.err
				})
			})
			require.ErrorIs(t, err, tc.err)
			var count int64
			err = database.handle.WithContext(ctx).Raw("SELECT count(*) FROM tenant WHERE code = ?", tc.name).Scan(&count).Error
			require.NoError(t, err)
			require.Equal(t, tc.count, count)
		})
	}
}

func TestMigrateIntegrationConcurrentStartup(t *testing.T) {
	database, ctx := integrationDB(t)
	var workers sync.WaitGroup
	errorsFound := make(chan error, 2)
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			state, err := database.Migrate(ctx, "up")
			if err == nil && (state.Version != 1 || state.Dirty) {
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

func TestPingIntegrationHonorsCancellation(t *testing.T) {
	database, ctx := integrationDB(t)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, database.Ping(canceled), context.Canceled)
	query, stop := database.WithContext(ctx)
	defer stop()
	deadline, ok := query.Statement.Context.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(3*time.Second), deadline, time.Second)
	require.True(t, database.handle.PrepareStmt)
	pool, err := database.handle.DB()
	require.NoError(t, err)
	require.Equal(t, 5, pool.Stats().MaxOpenConnections)
}

func TestAuditLogIntegrationAppendOnly(t *testing.T) {
	database, ctx := integrationDB(t)
	_, err := database.Migrate(ctx, "up")
	require.NoError(t, err)
	err = database.handle.WithContext(ctx).Exec(
		"INSERT INTO audit_log (actor_type, action, resource_type, resource_id, trace_id, result) VALUES ('system', 'test.verify', 'test', '1', 'trace-example', 'success')",
	).Error
	require.NoError(t, err)
	for _, query := range []string{"UPDATE audit_log SET result = 'failure'", "DELETE FROM audit_log"} {
		require.Error(t, database.handle.WithContext(ctx).Exec(query).Error)
	}
	var count int64
	require.NoError(t, database.handle.WithContext(ctx).Raw("SELECT count(*) FROM audit_log").Scan(&count).Error)
	require.EqualValues(t, 1, count)
}

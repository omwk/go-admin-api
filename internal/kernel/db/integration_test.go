package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

func integrationDB(t *testing.T) (*DB, context.Context) {
	return isolatedDB(t, true)
}

func isolatedDB(t *testing.T, migrated bool) (*DB, context.Context) {
	t.Helper()
	dsn := testenv.Lookup("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	cfg := config.DB{DSN: dsn, MaxOpen: 5, MaxIdle: 2, ConnMaxLifetime: time.Minute, TxnTimeout: testenv.Timeouts(t).DB.TxnTimeout, MigrateTimeout: testenv.Timeouts(t).DB.MigrateTimeout}
	if migrated {
		cfg.DSN = testenv.Database(ctx, t, dsn, func(ctx context.Context, isolated string) error {
			migrationConfig := cfg
			migrationConfig.DSN = isolated
			database, err := Open(ctx, migrationConfig)
			if err != nil {
				return err
			}
			_, err = database.Migrate(ctx, "up")
			return errors.Join(err, database.Close())
		})
	} else {
		cfg.DSN = testenv.EmptyDatabase(ctx, t, dsn)
	}
	database, err := Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	return database, ctx
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
				if err := database.Exec(txCtx, "INSERT INTO tenant (code, name) VALUES (?, ?)", tc.name, tc.name); err != nil {
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

func TestPingIntegrationHonorsCancellation(t *testing.T) {
	database, ctx := integrationDB(t)
	// Keep the existing three-second assertion checking the production default.
	database.config.TxnTimeout = config.DefaultTxnTimeout
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
	err = database.Exec(ctx,
		"INSERT INTO audit_log (actor_type, action, resource_type, resource_id, trace_id, result) VALUES ('system', 'test.verify', 'test', '1', 'trace-example', 'success')",
	)
	require.NoError(t, err)
	for _, query := range []string{"UPDATE audit_log SET result = 'failure'", "DELETE FROM audit_log"} {
		require.Error(t, database.Exec(ctx, query))
	}
	var count int64
	require.NoError(t, database.handle.WithContext(ctx).Raw("SELECT count(*) FROM audit_log").Scan(&count).Error)
	require.EqualValues(t, 1, count)
}

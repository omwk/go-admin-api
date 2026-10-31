package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

func integrationDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	dsn := testenv.Lookup("TEST_DATABASE_URL")
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
	// REQ-SYS-005: all embedded migrations remain repeatable and reversible.
	database, ctx := integrationDB(t)
	for _, command := range []string{"up", "up", "version"} {
		state, err := database.Migrate(ctx, command)
		require.NoError(t, err)
		require.Equal(t, MigrationVersion{Version: 4}, state)
	}
	var tables []string
	err := database.handle.WithContext(ctx).Raw(
		"SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_name <> 'schema_migrations' ORDER BY table_name",
	).Scan(&tables).Error
	require.NoError(t, err)
	require.Equal(t, []string{
		"audit_log", "credential_audit", "credential_version", "file_object", "idempotency_key", "notification", "outbox_event",
		"platform", "shop", "shop_credential",
		"sys_dept", "sys_menu", "sys_permission", "sys_refresh_token", "sys_role",
		"sys_role_dept", "sys_role_menu", "sys_role_permission", "sys_setting", "sys_user",
		"sys_user_role", "tenant",
	}, tables)
	t.Logf("up -> 4: tables=%d", len(tables))
	assertShopVersionColumn(ctx, t, database)
	var code string
	require.NoError(t, database.handle.WithContext(ctx).Raw("SELECT code FROM tenant WHERE id = 1").Scan(&code).Error)
	require.Equal(t, "default", code)
	state, err := database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 3}, state)
	assertShopMigrationRolledBack(ctx, t, database)
	state, err = database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 2}, state)
	logMigrationTableCount(ctx, t, database, "down -> 2", 17)
	var indexCount int64
	err = database.handle.WithContext(ctx).Raw("SELECT count(*) FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_refresh_session_revoked'").Scan(&indexCount).Error
	require.NoError(t, err)
	require.Zero(t, indexCount)
	state, err = database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 1}, state)
	err = database.handle.WithContext(ctx).Raw(
		"SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_name <> 'schema_migrations' ORDER BY table_name",
	).Scan(&tables).Error
	require.NoError(t, err)
	require.Equal(t, []string{"audit_log", "file_object", "idempotency_key", "notification", "outbox_event", "sys_setting", "tenant"}, tables)
	t.Logf("down -> 1: tables=%d", len(tables))
	state, err = database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Zero(t, state.Version)
	var count int64
	err = database.handle.WithContext(ctx).Raw(
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name <> 'schema_migrations'",
	).Scan(&count).Error
	require.NoError(t, err)
	require.Zero(t, count)
	t.Logf("down -> 0: tables=%d", count)
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 4}, state)
	logMigrationTableCount(ctx, t, database, "up -> 4", 22)
}

func assertShopVersionColumn(ctx context.Context, t *testing.T, database *DB) {
	t.Helper()
	var column struct{ DataType, IsNullable, ColumnDefault string }
	err := database.handle.WithContext(ctx).Raw(`SELECT data_type, is_nullable, column_default
		FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'shop' AND column_name = 'version'`).Scan(&column).Error
	require.NoError(t, err)
	require.Equal(t, "integer", column.DataType)
	require.Equal(t, "NO", column.IsNullable)
	require.Equal(t, "0", column.ColumnDefault)
}

func assertShopMigrationRolledBack(ctx context.Context, t *testing.T, database *DB) {
	t.Helper()
	var tables []string
	err := database.handle.WithContext(ctx).Raw(
		"SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_name <> 'schema_migrations' ORDER BY table_name",
	).Scan(&tables).Error
	require.NoError(t, err)
	require.Equal(t, []string{
		"audit_log", "file_object", "idempotency_key", "notification", "outbox_event",
		"sys_dept", "sys_menu", "sys_permission", "sys_refresh_token", "sys_role",
		"sys_role_dept", "sys_role_menu", "sys_role_permission", "sys_setting", "sys_user",
		"sys_user_role", "tenant",
	}, tables)
	var count int64
	err = database.handle.WithContext(ctx).Raw(`SELECT count(*) FROM pg_constraint c
		JOIN pg_namespace n ON n.oid = c.connamespace WHERE n.nspname = current_schema() AND c.contype = 'f'
		AND c.conname IN ('fk_shop_platform', 'fk_shop_owner', 'fk_shop_dept', 'fk_shop_credential_shop',
		'fk_credential_version_credential', 'fk_shop_credential_current_version', 'fk_credential_audit_credential')`).Scan(&count).Error
	require.NoError(t, err)
	require.Zero(t, count)
	t.Logf("down -> 3: tables=%d; 000004 foreign keys=%d", len(tables), count)
}

func logMigrationTableCount(ctx context.Context, t *testing.T, database *DB, stage string, expected int64) {
	t.Helper()
	var count int64
	err := database.handle.WithContext(ctx).Raw(
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name <> 'schema_migrations'",
	).Scan(&count).Error
	require.NoError(t, err)
	require.Equal(t, expected, count)
	t.Logf("%s: tables=%d", stage, count)
}

func TestIdentityMigrationIntegrationForeignKeys(t *testing.T) {
	// REQ-IAM-006: config references are tenant-bound; refresh history has no foreign key.
	database, ctx := integrationDB(t)
	_, err := database.Migrate(ctx, "up")
	require.NoError(t, err)
	var constraints []struct {
		Name       string
		TableName  string
		Definition string
	}
	err = database.handle.WithContext(ctx).Raw(`
		SELECT c.conname AS name, t.relname AS table_name, pg_get_constraintdef(c.oid) AS definition
		FROM pg_constraint c JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = current_schema() AND c.contype = 'f'
		ORDER BY c.conname`).Scan(&constraints).Error
	require.NoError(t, err)
	expected := map[string]string{
		"fk_sys_menu_permission":             "sys_permission(tenant_id, code)",
		"fk_sys_user_dept":                   "sys_dept(tenant_id, id)",
		"fk_sys_user_role_user":              "sys_user(tenant_id, id)",
		"fk_sys_user_role_role":              "sys_role(tenant_id, id)",
		"fk_sys_role_menu_role":              "sys_role(tenant_id, id)",
		"fk_sys_role_menu_menu":              "sys_menu(tenant_id, id)",
		"fk_sys_role_permission_role":        "sys_role(tenant_id, id)",
		"fk_sys_role_permission_permission":  "sys_permission(tenant_id, id)",
		"fk_sys_role_dept_role":              "sys_role(tenant_id, id)",
		"fk_sys_role_dept_dept":              "sys_dept(tenant_id, id)",
		"fk_shop_platform":                   "platform(id)",
		"fk_shop_owner":                      "sys_user(tenant_id, id)",
		"fk_shop_dept":                       "sys_dept(tenant_id, id)",
		"fk_shop_credential_shop":            "shop(tenant_id, id)",
		"fk_credential_version_credential":   "shop_credential(tenant_id, id)",
		"fk_shop_credential_current_version": "credential_version(tenant_id, credential_id, id)",
		"fk_credential_audit_credential":     "shop_credential(tenant_id, id)",
	}
	require.Len(t, constraints, len(expected))
	for _, constraint := range constraints {
		target, exists := expected[constraint.Name]
		require.True(t, exists, constraint.Name)
		if constraint.Name == "fk_shop_platform" {
			require.Contains(t, constraint.Definition, "FOREIGN KEY (platform_id)")
		} else {
			require.Contains(t, constraint.Definition, "FOREIGN KEY (tenant_id, ")
		}
		require.Contains(t, constraint.Definition, "REFERENCES "+target)
		require.Contains(t, constraint.Definition, "ON DELETE RESTRICT")
		require.NotEqual(t, "sys_refresh_token", constraint.TableName)
	}
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

func TestMigrateIntegrationConcurrentStartup(t *testing.T) {
	database, ctx := integrationDB(t)
	var workers sync.WaitGroup
	errorsFound := make(chan error, 2)
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			state, err := database.Migrate(ctx, "up")
			if err == nil && (state.Version != 4 || state.Dirty) {
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

package testenv_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

func runtimeAdmin(t *testing.T) (context.Context, string, *pgx.Conn) {
	t.Helper()
	dsn := testenv.Lookup("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close(ctx)) })
	return ctx, dsn, conn
}

func realMigrations(t *testing.T, calls *atomic.Int32) func(context.Context, string) error {
	t.Helper()
	cfg := testenv.Timeouts(t).DB
	return func(ctx context.Context, dsn string) error {
		calls.Add(1)
		local := cfg
		local.DSN, local.MaxOpen, local.MaxIdle, local.ConnMaxLifetime = dsn, 5, 2, time.Minute
		database, err := db.Open(ctx, local)
		if err != nil {
			return err
		}
		_, err = database.Migrate(ctx, "up")
		return errors.Join(err, database.Close())
	}
}

func TestDatabaseIntegrationClonesAreIsolatedAndDropped(t *testing.T) {
	ctx, dsn, admin := runtimeAdmin(t)
	var calls atomic.Int32
	migrate := realMigrations(t, &calls)
	var names []string
	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			isolated := testenv.Database(ctx, t, dsn, migrate)
			conn, err := pgx.Connect(ctx, isolated)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close(ctx)) })
			var database string
			require.NoError(t, conn.QueryRow(ctx, "SELECT current_database()").Scan(&database))
			names = append(names, database)
			var count int
			require.NoError(t, conn.QueryRow(ctx, "SELECT count(*) FROM sys_user").Scan(&count))
			require.Zero(t, count)
			_, err = conn.Exec(ctx, "INSERT INTO tenant (code,name) VALUES ('isolation-probe','test')")
			require.NoError(t, err)
		})
	}
	var canCreate bool
	require.NoError(t, admin.QueryRow(ctx, "SELECT rolcreatedb OR rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&canCreate))
	if canCreate {
		require.NotEqual(t, names[0], names[1])
		for _, name := range names {
			var exists bool
			require.NoError(t, admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists))
			require.False(t, exists)
		}
		require.LessOrEqual(t, calls.Load(), int32(1)) // A prior process may already have built the hash.
	} else {
		require.EqualValues(t, 2, calls.Load())
	}
}

func TestDatabaseIntegrationWithoutCreateDB(t *testing.T) {
	ctx, dsn, admin := runtimeAdmin(t)
	var super bool
	require.NoError(t, admin.QueryRow(ctx, "SELECT rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&super))
	var canCreate bool
	require.NoError(t, admin.QueryRow(ctx, "SELECT rolcreatedb FROM pg_roles WHERE rolname=current_user").Scan(&canCreate))
	if !super && canCreate {
		t.Skip("use a no-CREATEDB TEST_DATABASE_URL to verify fallback; CI administrator creates a restricted role")
	}
	if super {
		dsn = noCreateRole(ctx, t, dsn, admin)
	}
	var calls atomic.Int32
	migrate := realMigrations(t, &calls)
	for i := 0; i < 2; i++ {
		t.Run("fallback", func(t *testing.T) {
			isolated := testenv.Database(ctx, t, dsn, migrate)
			conn, err := pgx.Connect(ctx, isolated)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close(ctx)) })
			var current string
			var canCreate bool
			require.NoError(t, conn.QueryRow(ctx, "SELECT current_user,rolcreatedb OR rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&current, &canCreate))
			require.NotEmpty(t, current)
			require.False(t, canCreate)
			var version int
			require.NoError(t, conn.QueryRow(ctx, "SELECT version FROM schema_migrations WHERE NOT dirty").Scan(&version))
			require.Positive(t, version)
		})
	}
	require.EqualValues(t, 2, calls.Load())
}

func noCreateRole(ctx context.Context, t *testing.T, dsn string, admin *pgx.Conn) string {
	t.Helper()
	role := "s2_role_" + strings.ToLower(ulid.Make().String())
	quoted := pgx.Identifier{role}.Sanitize()
	_, err := admin.Exec(ctx, "CREATE ROLE "+quoted+" NOLOGIN NOSUPERUSER NOCREATEDB")
	require.NoError(t, err)
	var database string
	require.NoError(t, admin.QueryRow(ctx, "SELECT current_database()").Scan(&database))
	_, err = admin.Exec(ctx, "GRANT CREATE ON DATABASE "+pgx.Identifier{database}.Sanitize()+" TO "+quoted)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec(ctx, "DROP OWNED BY "+quoted)
		require.NoError(t, err)
		_, err = admin.Exec(ctx, "DROP ROLE "+quoted)
		require.NoError(t, err)
	})
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("role", role)
		parsed.RawQuery = query.Encode()
		dsn = parsed.String()
	} else {
		dsn += " role=" + role
	}
	return dsn
}

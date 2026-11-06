package testenv

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func templateAdmin(t *testing.T) (context.Context, string, *pgx.Conn) {
	t.Helper()
	dsn := Lookup("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	conn, err := connectAdmin(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { closeAdmin(ctx, t, conn) })
	var canCreate bool
	require.NoError(t, conn.QueryRow(ctx, "SELECT rolcreatedb OR rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&canCreate))
	if !canCreate {
		t.Skip("template lifecycle test requires CREATEDB; fallback is tested separately")
	}
	unlock, err := templateLock(ctx, conn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, unlock()) })
	return ctx, dsn, conn
}

func TestTemplateIntegrationExpiredDatabasesOnly(t *testing.T) {
	ctx, _, admin := templateAdmin(t)
	now := time.Now()
	expired := fmt.Sprintf("commercehub_test_%d_%s", now.Add(-2*time.Hour).Unix(), newSuffix())
	recent := fmt.Sprintf("commercehub_test_%d_%s", now.Unix(), newSuffix())
	oldTemplate := "commercehub_tpl_" + newSuffix()
	active := fmt.Sprintf("commercehub_test_%d_%s", now.Add(-2*time.Hour).Unix(), newSuffix())
	for _, name := range []string{expired, recent, oldTemplate, active} {
		_, err := admin.Exec(ctx, "CREATE DATABASE "+quote(name)+" TEMPLATE template0")
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, dropDatabase(ctx, admin, name))
		})
	}
	comment := fmt.Sprintf("commercehub-test-runtime:%d:ready", now.Add(-2*time.Hour).Unix())
	_, err := admin.Exec(ctx, "COMMENT ON DATABASE "+quote(oldTemplate)+" IS '"+comment+"'")
	require.NoError(t, err)
	conn, err := pgx.Connect(ctx, WithDatabase(t, Lookup("TEST_DATABASE_URL"), active, "public"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close(ctx)) })
	require.NoError(t, cleanStale(ctx, admin, now))
	for _, tc := range []struct {
		name   string
		exists bool
	}{{expired, false}, {oldTemplate, false}, {recent, true}, {active, true}} {
		var exists bool
		require.NoError(t, admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", tc.name).Scan(&exists))
		require.Equal(t, tc.exists, exists, tc.name)
	}
}

func TestTemplateIntegrationFailedBuildNeverSurvives(t *testing.T) {
	ctx, dsn, admin := templateAdmin(t)
	manager := &templateManager{name: "commercehub_tpl_" + newSuffix()}
	injected := errors.New("injected migration failure")
	err := manager.build(ctx, t, admin, dsn, func(context.Context, string) error { return injected })
	require.ErrorIs(t, err, injected)
	var exists bool
	require.NoError(t, admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", manager.name).Scan(&exists))
	require.False(t, exists)
}

package testenv

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"
)

type templateManager struct {
	once     sync.Once
	warning  sync.Once
	name     string
	fallback bool
	err      error
}

var templates sync.Map

// Database clones a template built by the caller's real migration chain.
// Register pool cleanup after this call so pools close before the database drops.
func Database(ctx context.Context, t testing.TB, dsn string, migrate func(context.Context, string) error) string {
	t.Helper()
	value, _ := templates.LoadOrStore(dsn, &templateManager{})
	manager := value.(*templateManager)
	manager.once.Do(func() { manager.err = manager.initialize(ctx, t, dsn, migrate) })
	if manager.err != nil {
		t.Fatalf("initialize test template: %v", manager.err)
	}
	if !manager.fallback {
		cloned, err := manager.clone(ctx, t, dsn)
		if err == nil {
			return cloned
		}
		if !permissionDenied(err) {
			t.Fatalf("clone test template: %v", err)
		}
	}
	manager.warning.Do(func() { t.Log("WARNING test database: no CREATEDB permission; using schema + real migrations") })
	isolated := EmptyDatabase(ctx, t, dsn)
	if err := migrate(ctx, isolated); err != nil {
		t.Fatalf("migrate fallback schema: %v", err)
	}
	return isolated
}

// EmptyDatabase creates an unmigrated schema for tests of the migration runner.
func EmptyDatabase(ctx context.Context, t testing.TB, dsn string) string {
	t.Helper()
	admin, err := connectAdmin(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "s2_" + newSuffix()
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+quote(name))
	if err != nil {
		closeAdmin(ctx, t, admin)
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		cleanup, cancel := cleanupContext(ctx)
		defer cancel()
		_, dropErr := admin.Exec(cleanup, "DROP SCHEMA "+quote(name)+" CASCADE")
		if err := errors.Join(dropErr, admin.Close(cleanup)); err != nil {
			t.Errorf("clean test schema: %v", err)
		}
	})
	return WithDatabase(t, dsn, "", name)
}

func (m *templateManager) clone(ctx context.Context, t testing.TB, dsn string) (string, error) {
	admin, err := connectAdmin(ctx, dsn)
	if err != nil {
		return "", err
	}
	defer closeAdmin(ctx, t, admin)
	unlock, err := templateLock(ctx, admin)
	if err != nil {
		return "", err
	}
	defer func() { reportCleanup(t, unlock()) }()
	name := fmt.Sprintf("commercehub_test_%d_%s", time.Now().Unix(), newSuffix())
	_, err = admin.Exec(ctx, "CREATE DATABASE "+quote(name)+" TEMPLATE "+quote(m.name))
	if err != nil {
		return "", err
	}
	t.Cleanup(func() {
		cleanup, cancel := cleanupContext(ctx)
		defer cancel()
		conn, err := connectAdmin(cleanup, dsn)
		if err != nil {
			t.Errorf("connect for test database cleanup: %v", err)
			return
		}
		defer closeAdmin(cleanup, t, conn)
		reportCleanup(t, dropDatabase(cleanup, conn, name))
	})
	// Refresh the lease so another process never expires an actively used template.
	if err := markTemplate(ctx, admin, m.name, "ready"); err != nil {
		return "", err
	}
	t.Logf("test database template clone: %s", name)
	return WithDatabase(t, dsn, name, "public"), nil
}

func newSuffix() string { return strings.ToLower(ulid.Make().String()) }

func dropDatabase(ctx context.Context, conn *pgx.Conn, name string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+quote(name))
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55006" {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-ticker.C:
		}
	}
}

func quote(name string) string { return pgx.Identifier{name}.Sanitize() }

func connectAdmin(ctx context.Context, dsn string) (*pgx.Conn, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, errors.New("connect test database administrator")
	}
	return conn, nil
}

func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
}

func closeAdmin(ctx context.Context, t testing.TB, conn *pgx.Conn) {
	t.Helper()
	cleanup, cancel := cleanupContext(ctx)
	defer cancel()
	reportCleanup(t, conn.Close(cleanup))
}

func reportCleanup(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("clean test database: %v", err)
	}
}

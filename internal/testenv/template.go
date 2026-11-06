package testenv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omwk/go-admin-api/migrations"
)

func (m *templateManager) initialize(ctx context.Context, t testing.TB, dsn string, migrate func(context.Context, string) error) error {
	admin, err := connectAdmin(ctx, dsn)
	if err != nil {
		return err
	}
	defer closeAdmin(ctx, t, admin)
	var canCreate bool
	if err := admin.QueryRow(ctx, "SELECT rolcreatedb OR rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&canCreate); err != nil {
		return err
	}
	if !canCreate {
		m.fallback = true
		return nil
	}
	unlock, err := templateLock(ctx, admin)
	if err != nil {
		return err
	}
	defer func() { reportCleanup(t, unlock()) }()
	if err := cleanStale(ctx, admin, time.Now()); err != nil {
		return err
	}
	digest, err := migrationHash(migrations.Files)
	if err != nil {
		return err
	}
	m.name = "commercehub_tpl_" + digest
	var ready bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database
		WHERE datname=$1 AND shobj_description(oid,'pg_database') LIKE 'commercehub-test-runtime:%:ready')`, m.name).Scan(&ready); err != nil {
		return err
	}
	if ready {
		return markTemplate(ctx, admin, m.name, "ready")
	}
	return m.build(ctx, t, admin, dsn, migrate)
}

func (m *templateManager) build(ctx context.Context, t testing.TB, admin *pgx.Conn, dsn string, migrate func(context.Context, string) error) (err error) {
	// An interrupted build has no ready marker and must never be cloned.
	if _, err = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+quote(m.name)); err != nil {
		return err
	}
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quote(m.name)+" TEMPLATE template0"); err != nil {
		if permissionDenied(err) {
			m.fallback = true
			return nil
		}
		return err
	}
	defer func() {
		if err != nil {
			cleanup, cancel := cleanupContext(ctx)
			defer cancel()
			dropErr := dropDatabase(cleanup, admin, m.name)
			err = errors.Join(err, dropErr)
		}
	}()
	if err = markTemplate(ctx, admin, m.name, "building"); err != nil {
		return err
	}
	if err = migrate(ctx, WithDatabase(t, dsn, m.name, "public")); err != nil {
		return err
	}
	if err = markTemplate(ctx, admin, m.name, "ready"); err == nil {
		t.Logf("test database template build: %s (real migration chain)", m.name)
	}
	return err
}

func templateLock(ctx context.Context, conn *pgx.Conn) (func() error, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var locked bool
		if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended('commercehub-test-runtime',0))").Scan(&locked); err != nil {
			return nil, err
		}
		if locked {
			return func() error {
				cleanup, cancel := cleanupContext(ctx)
				defer cancel()
				_, err := conn.Exec(cleanup, "SELECT pg_advisory_unlock(hashtextextended('commercehub-test-runtime',0))")
				return err
			}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func markTemplate(ctx context.Context, conn *pgx.Conn, name, state string) error {
	comment := fmt.Sprintf("commercehub-test-runtime:%d:%s", time.Now().Unix(), state)
	_, err := conn.Exec(ctx, "COMMENT ON DATABASE "+quote(name)+" IS '"+comment+"'")
	return err
}

func cleanStale(ctx context.Context, conn *pgx.Conn, now time.Time) error {
	rows, err := conn.Query(ctx, `SELECT datname, COALESCE(shobj_description(oid,'pg_database'),'')
		FROM pg_database WHERE datdba=(SELECT oid FROM pg_roles WHERE rolname=current_user)
		AND (datname LIKE 'commercehub\_test\_%' ESCAPE '\' OR datname LIKE 'commercehub\_tpl\_%' ESCAPE '\')
		AND NOT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datid=pg_database.oid)`)
	if err != nil {
		return err
	}
	type candidate struct{ name, comment string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.name, &c.comment); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range candidates {
		if staleDatabase(c.name, c.comment, now) {
			if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+quote(c.name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func staleDatabase(name, comment string, now time.Time) bool {
	var value string
	if strings.HasPrefix(name, "commercehub_test_") {
		value, _, _ = strings.Cut(strings.TrimPrefix(name, "commercehub_test_"), "_")
	} else if strings.HasPrefix(name, "commercehub_tpl_") && strings.HasPrefix(comment, "commercehub-test-runtime:") {
		value, _, _ = strings.Cut(strings.TrimPrefix(comment, "commercehub-test-runtime:"), ":")
	}
	created, err := strconv.ParseInt(value, 10, 64)
	return err == nil && now.Sub(time.Unix(created, 0)) > time.Hour
}

func migrationHash(files fs.FS) (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".sql") {
			return err
		}
		data, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		// Include both path and content with boundaries; WalkDir sorts by name.
		_, err = fmt.Fprintf(hash, "%s\x00%d\x00%s", path, len(data), data)
		return err
	})
	return hex.EncodeToString(hash.Sum(nil))[:32], err
}

func permissionDenied(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

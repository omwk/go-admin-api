package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/omwk/go-admin-api/migrations"
)

// MigrationVersion describes the current schema state; zero means uninitialized.
type MigrationVersion struct {
	Version uint
	Dirty   bool
}

// Migrate applies all pending migrations, rolls back one, or reads the version.
// The PostgreSQL driver uses advisory locks for initialization and migration runs.
func (d *DB) Migrate(ctx context.Context, command string) (state MigrationVersion, err error) {
	if command != "up" && command != "down" && command != "version" {
		return state, errors.New("migrate command must be up, down or version")
	}
	timeout := d.config.MigrationTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pool, err := d.handle.DB()
	if err != nil {
		return state, errors.New("access database pool for migration")
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return state, errors.New("connect database for migration")
	}
	defer func() { err = errors.Join(err, resetMigrationConnection(ctx, conn, timeout)) }()
	if _, err = conn.ExecContext(ctx, "SET statement_timeout = 0; SET lock_timeout = 0"); err != nil {
		return state, errors.New("configure migration timeouts")
	}
	unlock, err := acquireMigrationGate(ctx, conn, timeout)
	if err != nil {
		return state, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	driver, err := postgres.WithConnection(ctx, conn, &postgres.Config{})
	if err != nil {
		return state, fmt.Errorf("initialize migration driver: %w", err)
	}
	source, err := iofs.New(migrations.Files, ".")
	if err != nil {
		return state, fmt.Errorf("open embedded migrations: %w", err)
	}
	defer func() { err = errors.Join(err, source.Close()) }()
	runner, err := migrate.NewWithInstance("iofs", source, "postgres", &migrationContextDriver{Driver: driver, ctx: ctx, conn: conn})
	if err != nil {
		return state, fmt.Errorf("initialize migration runner: %w", err)
	}
	return runMigration(runner, command)
}

func runMigration(runner *migrate.Migrate, command string) (state MigrationVersion, err error) {
	switch command {
	case "up":
		err = runner.Up()
	case "down":
		err = runner.Steps(-1)
	}
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return state, fmt.Errorf("migrate %s: %w", command, err)
	}
	state.Version, state.Dirty, err = runner.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		err = nil
	}
	return state, err
}

func resetMigrationConnection(ctx context.Context, conn *sql.Conn, timeout time.Duration) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	_, resetErr := conn.ExecContext(cleanupCtx, "ROLLBACK; RESET statement_timeout; RESET lock_timeout")
	return errors.Join(resetErr, conn.Close())
}

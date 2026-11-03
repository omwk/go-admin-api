package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

// DB owns a PostgreSQL connection pool.
type DB struct {
	handle *gorm.DB
}

// Open initializes GORM, configures the pool and verifies connectivity.
func Open(ctx context.Context, cfg config.DB) (*DB, error) {
	if cfg.MaxOpen <= 0 || cfg.MaxIdle < 0 || cfg.MaxIdle > cfg.MaxOpen || cfg.ConnMaxLifetime <= 0 {
		return nil, errors.New("db pool settings are invalid")
	}
	handle, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		PrepareStmt: true, DisableAutomaticPing: true, Logger: gormlog.Default.LogMode(gormlog.Silent),
	})
	if err != nil {
		return nil, errors.New("initialize db.dsn: invalid PostgreSQL configuration")
	}
	pool, err := handle.DB()
	if err != nil {
		return nil, errors.New("initialize database pool")
	}
	pool.SetMaxOpenConns(cfg.MaxOpen)
	pool.SetMaxIdleConns(cfg.MaxIdle)
	pool.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	database := &DB{handle: handle}
	if err := database.Ping(ctx); err != nil {
		return nil, errors.Join(err, database.Close())
	}
	return database, nil
}

// Ping verifies connectivity within two seconds or the caller's earlier deadline.
func (d *DB) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	pool, err := d.handle.DB()
	if err != nil {
		return errors.New("access database pool")
	}
	if err := pool.PingContext(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("database ping failed")
	}
	return nil
}

// Close releases prepared statements and pool connections.
func (d *DB) Close() error {
	if prepared, ok := d.handle.ConnPool.(*gorm.PreparedStmtDB); ok {
		prepared.Close()
	}
	pool, err := d.handle.DB()
	if err != nil {
		return errors.New("access database pool for close")
	}
	return pool.Close()
}

type transactionKey struct{ database *DB }

type transactionMarkerKey struct{}

// InTransaction reports whether ctx carries a database transaction.
func InTransaction(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, ok := ctx.Value(transactionMarkerKey{}).(bool)
	return ok
}

// WithContext returns the active transaction or pool with a three-second deadline.
// Callers must release the returned cancel function after completing their query.
func (d *DB) WithContext(ctx context.Context) (*gorm.DB, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	if tx, ok := ctx.Value(transactionKey{d}).(*gorm.DB); ok {
		return tx.WithContext(ctx), cancel
	}
	return d.handle.WithContext(ctx), cancel
}

// Exec runs a statement on the active transaction or database pool.
func (d *DB) Exec(ctx context.Context, query string, args ...any) error {
	if strings.TrimSpace(query) == "" {
		return errors.New("database statement is required")
	}
	handle, cancel := d.WithContext(ctx)
	defer cancel()
	if err := handle.Exec(query, args...).Error; err != nil {
		return fmt.Errorf("execute database statement: %w", err)
	}
	return nil
}

// Transaction runs fn in one transaction; nested calls reuse it through context.
// The callback must propagate errors to the outer boundary to trigger rollback.
func (d *DB) Transaction(ctx context.Context, fn func(context.Context) error) error {
	if _, ok := ctx.Value(transactionKey{d}).(*gorm.DB); ok {
		return fn(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return d.handle.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := context.WithValue(ctx, transactionKey{d}, tx.WithContext(ctx))
		txCtx = context.WithValue(txCtx, transactionMarkerKey{}, true)
		return fn(txCtx)
	})
}

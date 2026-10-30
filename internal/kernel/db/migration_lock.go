package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// acquireMigrationGate serializes driver initialization without a waiting SQL snapshot.
// A blocking advisory-lock query can deadlock CREATE INDEX CONCURRENTLY, which
// waits for old snapshots; poll the session lock and wait on the caller side instead.
func acquireMigrationGate(ctx context.Context, conn *sql.Conn) (func() error, error) {
	var key int64
	err := conn.QueryRowContext(ctx, `SELECT hashtextextended(current_database() || ':' || current_schema() || ':kernel-migration', 0)`).Scan(&key)
	if err != nil {
		return nil, fmt.Errorf("resolve migration lock identity: %w", err)
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var locked bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked); err != nil {
			return nil, err
		}
		if locked {
			return func() error {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				defer cancel()
				_, err := conn.ExecContext(cleanup, "SELECT pg_advisory_unlock($1)", key)
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

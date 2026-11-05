package db

import (
	"context"
	"database/sql"
	"io"
	"strings"

	"github.com/golang-migrate/migrate/v4/database"
)

// The upstream PostgreSQL driver's Run creates a background context. Bind SQL
// execution to the migration caller instead, keeping each migration file intact.
type migrationContextDriver struct {
	database.Driver
	ctx  context.Context
	conn *sql.Conn
}

func (d *migrationContextDriver) Run(reader io.Reader) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	query, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(query)) == "" {
		return nil
	}
	if _, err := d.conn.ExecContext(d.ctx, string(query)); err != nil {
		return &database.Error{OrigErr: err, Err: "migration failed", Query: query}
	}
	return nil
}

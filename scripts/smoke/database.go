package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/migrations"
)

func schemaName() (string, error) {
	schema := os.Getenv("SMOKE_SCHEMA")
	if !schemaPattern.MatchString(schema) {
		return "", errors.New("invalid smoke schema")
	}
	return schema, nil
}

func connectionConfig(scoped bool) (*pgx.ConnConfig, error) {
	schema, err := schemaName()
	if err != nil {
		return nil, err
	}
	cfg, err := pgx.ParseConfig(os.Getenv("SMOKE_DATABASE_URL"))
	if err != nil {
		return nil, err
	}
	cfg.ConnectTimeout = 3 * time.Second
	cfg.RuntimeParams["search_path"] = "pg_catalog"
	if scoped {
		// No public fallback: missing tables must never reach development data.
		cfg.RuntimeParams["search_path"] = schema
	}
	cfg.RuntimeParams["application_name"] = schema
	return cfg, nil
}

func connect(ctx context.Context, scoped bool) (*pgx.Conn, error) {
	cfg, err := connectionConfig(scoped)
	if err != nil {
		return nil, err
	}
	return pgx.ConnectConfig(ctx, cfg)
}

func scopedDSN() string {
	raw := os.Getenv("SMOKE_DATABASE_URL")
	schema := os.Getenv("SMOKE_SCHEMA")
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err == nil {
			query := parsed.Query()
			query.Set("search_path", schema)
			query.Set("application_name", schema)
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
	}
	return raw + " search_path='" + schema + "' application_name='" + schema + "'"
}

func createSchema(ctx context.Context) (result any, err error) {
	c, err := connect(ctx, false)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, c.Close(ctx)) }()
	schema, err := schemaName()
	if err != nil {
		return nil, err
	}
	_, err = c.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	if err != nil {
		return nil, err
	}
	// The DSN is returned over the captured pipe only; never persisted or printed.
	return map[string]any{"dsn": scopedDSN(), "schema": schema}, nil
}

func dropSchema(ctx context.Context) (result any, err error) {
	c, err := connect(ctx, false)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, c.Close(ctx)) }()
	schema, err := schemaName()
	if err != nil {
		return nil, err
	}
	_, err = c.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	if err != nil {
		return nil, err
	}
	var remaining int
	err = c.QueryRow(ctx, "SELECT count(*) FROM pg_namespace WHERE nspname=$1", schema).Scan(&remaining)
	return map[string]any{"remaining_schemas": remaining}, err
}

func migrateSchema(ctx context.Context) (result any, err error) {
	if _, err := schemaName(); err != nil {
		return nil, err
	}
	database, err := db.Open(ctx, config.DB{DSN: scopedDSN(), MaxOpen: 3, MaxIdle: 1, ConnMaxLifetime: time.Minute, MigrateTimeout: 20 * time.Second})
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	for _, command := range []string{"up", "down", "up"} {
		if _, err := database.Migrate(ctx, command); err != nil {
			return nil, err
		}
	}
	state, err := database.Migrate(ctx, "version")
	if err != nil {
		return nil, err
	}
	files, err := fs.Glob(migrations.Files, "*.up.sql")
	if err != nil {
		return nil, err
	}
	latest := 0
	for _, file := range files {
		number, err := strconv.Atoi(strings.SplitN(file, "_", 2)[0])
		if err != nil {
			return nil, err
		}
		latest = max(latest, number)
	}
	return map[string]any{"version": state.Version, "latest": latest, "dirty": state.Dirty}, nil
}

func queryJSON(ctx context.Context) (result any, err error) {
	var input struct {
		SQL  string `json:"sql"`
		Args []any  `json:"args"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return nil, err
	}
	c, err := connect(ctx, true)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, c.Close(ctx)) }()
	var raw json.RawMessage
	err = c.QueryRow(ctx, input.SQL, input.Args...).Scan(&raw)
	return raw, err
}

package testenv

import (
	"net/url"
	"strings"
	"testing"
)

// WithDatabase preserves connection settings while selecting a database/schema.
// An empty database keeps the original database (schema fallback).
func WithDatabase(t testing.TB, dsn, database, schema string) string {
	t.Helper()
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal("parse test database connection settings")
		}
		if database != "" {
			parsed.Path = "/" + database
			parsed.RawPath = ""
		}
		query := parsed.Query()
		if database != "" {
			query.Del("dbname")
		}
		query.Set("search_path", schema+",public")
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	if database != "" {
		dsn += " dbname='" + strings.ReplaceAll(strings.ReplaceAll(database, `\`, `\\`), "'", `\'`) + "'"
	}
	return dsn + " search_path='" + strings.ReplaceAll(schema, "'", `\'`) + ",public'"
}

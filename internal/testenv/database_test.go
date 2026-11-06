package testenv

import (
	"fmt"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestMigrationHashTracksBothDirectionsAndNames(t *testing.T) {
	files := fstest.MapFS{"001.up.sql": {Data: []byte("up")}, "001.down.sql": {Data: []byte("down")}}
	first, err := migrationHash(files)
	require.NoError(t, err)
	files["notes.md"] = &fstest.MapFile{Data: []byte("ignored")}
	same, err := migrationHash(files)
	require.NoError(t, err)
	require.Equal(t, first, same)
	files["001.down.sql"].Data = []byte("changed")
	changed, err := migrationHash(files)
	require.NoError(t, err)
	require.NotEqual(t, first, changed)
	files["002.up.sql"] = files["001.up.sql"]
	delete(files, "001.up.sql")
	renamed, err := migrationHash(files)
	require.NoError(t, err)
	require.NotEqual(t, changed, renamed)
}

func TestStaleDatabaseRequiresOwnedPrefixAndExpiredTimestamp(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		name, comment string
		stale         bool
	}{
		{fmt.Sprintf("commercehub_test_%d_id", now.Add(-2*time.Hour).Unix()), "", true},
		{fmt.Sprintf("commercehub_test_%d_id", now.Add(-time.Hour).Unix()), "", false},
		{"commercehub_tpl_hash", fmt.Sprintf("commercehub-test-runtime:%d:ready", now.Add(-2*time.Hour).Unix()), true},
		{"commercehub_tpl_hash", "unrelated", false},
		{"commercehub_test_invalid", "", false},
		{"production", "commercehub-test-runtime:1:ready", false},
	} {
		require.Equal(t, tc.stale, staleDatabase(tc.name, tc.comment, now), tc.name)
	}
}

func TestWithDatabasePreservesConnectionOptions(t *testing.T) {
	for _, dsn := range []string{
		"postgres://localhost/original?sslmode=disable&application_name=runtime",
		"host=localhost dbname=original sslmode=disable application_name=runtime",
	} {
		changed := WithDatabase(t, dsn, "cloned", "isolated")
		cfg, err := pgx.ParseConfig(changed)
		require.NoError(t, err)
		require.Equal(t, "cloned", cfg.Database)
		require.Equal(t, "isolated,public", cfg.RuntimeParams["search_path"])
		require.Equal(t, "runtime", cfg.RuntimeParams["application_name"])
		fallback, err := pgx.ParseConfig(WithDatabase(t, dsn, "", "fallback"))
		require.NoError(t, err)
		require.Equal(t, "original", fallback.Database)
	}
}

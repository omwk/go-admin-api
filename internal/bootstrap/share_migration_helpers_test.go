package bootstrap

import (
	"context"
	"io/fs"
	"strconv"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/migrations"
	"github.com/stretchr/testify/require"
)

func latestBootstrapMigration(t *testing.T) uint {
	t.Helper()
	files, err := fs.Glob(migrations.Files, "*.up.sql")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	number, _, ok := strings.Cut(files[len(files)-1], "_")
	require.True(t, ok)
	version, err := strconv.ParseUint(number, 10, 32)
	require.NoError(t, err)
	return uint(version)
}

func migrationAt(ctx context.Context, t *testing.T, database *db.DB, target uint) db.MigrationVersion {
	t.Helper()
	state, err := database.Migrate(ctx, "version")
	require.NoError(t, err)
	for state.Version > target {
		state, err = database.Migrate(ctx, "down")
		require.NoError(t, err)
		require.False(t, state.Dirty)
	}
	require.Equal(t, target, state.Version)
	return state
}

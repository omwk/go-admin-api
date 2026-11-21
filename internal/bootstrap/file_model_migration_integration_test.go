package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileModelIntegrationMigrationDefaultsAndReversibility(t *testing.T) {
	f := newFileStorageFixture(t)
	provider := f.save(t, f.input, 0, "")
	state, err := f.database.Migrate(f.ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 38, state.Version)
	state, err = f.database.Migrate(f.ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, 39, state.Version)
	require.False(t, state.Dirty)
	persisted, err := f.store.Provider(f.ctx, provider.ID)
	require.NoError(t, err)
	require.Equal(t, "public", persisted.AccessMode)
	require.EqualValues(t, 1024, persisted.QuotaPerUserMB)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var nullable int64
	require.NoError(t, q.Raw(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='file_object' AND column_name IN ('category','thumb_key') AND is_nullable='YES'`).Scan(&nullable).Error)
	require.EqualValues(t, 2, nullable)
	t.Log("BE-IDN-025 migration: down=38; up=39 dirty=false; existing provider defaults public/1024; category/thumb_key nullable")
}

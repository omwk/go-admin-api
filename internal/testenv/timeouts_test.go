package testenv

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimeoutsOnlyOverrideIntegrationDefaults(t *testing.T) {
	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(t.TempDir()))
	t.Cleanup(func() { require.NoError(t, os.Chdir(previous)) })
	for _, key := range []string{"CH_DB_TXN_TIMEOUT", "CH_CACHE_OP_TIMEOUT", "CH_DB_MIGRATE_TIMEOUT", "TEST_DATABASE_URL"} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	cfg := Timeouts(t)
	require.Equal(t, 3*time.Second, cfg.DB.TxnTimeout)
	require.Equal(t, time.Second, cfg.Cache.OpTimeout)
	require.Equal(t, 30*time.Second, cfg.DB.MigrateTimeout)
	t.Setenv("TEST_DATABASE_URL", "postgres://localhost/test")
	cfg = Timeouts(t)
	require.Equal(t, 15*time.Second, cfg.DB.TxnTimeout)
	require.Equal(t, 5*time.Second, cfg.Cache.OpTimeout)
	require.Equal(t, 5*time.Minute, cfg.DB.MigrateTimeout)
	t.Setenv("CH_DB_TXN_TIMEOUT", "7s")
	t.Setenv("CH_CACHE_OP_TIMEOUT", "2500ms")
	t.Setenv("CH_DB_MIGRATE_TIMEOUT", "2m")
	cfg = Timeouts(t)
	require.Equal(t, 7*time.Second, cfg.DB.TxnTimeout)
	require.Equal(t, 2500*time.Millisecond, cfg.Cache.OpTimeout)
	require.Equal(t, 2*time.Minute, cfg.DB.MigrateTimeout)
	t.Setenv("CH_DB_TXN_TIMEOUT", "invalid")
	t.Setenv("CH_DB_MIGRATE_TIMEOUT", "invalid")
	cfg = Timeouts(t)
	require.Equal(t, 3*time.Second, cfg.DB.TxnTimeout)
	require.Equal(t, 30*time.Second, cfg.DB.MigrateTimeout)
}

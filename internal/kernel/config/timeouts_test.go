package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimeoutBudgets(t *testing.T) {
	// S1: report effective budgets without requiring a database connection.
	cfg, err := Load("")
	require.NoError(t, err)
	t.Logf("db.txn_timeout=%s cache.op_timeout=%s", cfg.DB.TxnTimeout, cfg.Cache.OpTimeout)
}

func TestTimeoutConfigurationPriority(t *testing.T) {
	for _, key := range []string{"CH_DB_TXN_TIMEOUT", "CH_CACHE_OP_TIMEOUT"} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, 3*time.Second, cfg.DB.TxnTimeout)
	require.Equal(t, time.Second, cfg.Cache.OpTimeout)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("db:\n  txn_timeout: 4s\ncache:\n  op_timeout: 1500ms\n"), 0o600))
	cfg, err = Load(path)
	require.NoError(t, err)
	require.Equal(t, 4*time.Second, cfg.DB.TxnTimeout)
	require.Equal(t, 1500*time.Millisecond, cfg.Cache.OpTimeout)
	t.Setenv("CH_DB_TXN_TIMEOUT", "15s")
	t.Setenv("CH_CACHE_OP_TIMEOUT", "5s")
	cfg, err = Load(path)
	require.NoError(t, err)
	require.Equal(t, 15*time.Second, cfg.DB.TxnTimeout)
	require.Equal(t, 5*time.Second, cfg.Cache.OpTimeout)
}

func TestInvalidTimeoutsFallBackWithOneSafeWarning(t *testing.T) {
	for _, value := range []string{"invalid-private-value", "", "0s", "-1s", "999999999999999999999h"} {
		t.Run(value, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			t.Setenv("CH_DB_TXN_TIMEOUT", value)
			t.Setenv("CH_CACHE_OP_TIMEOUT", value)
			cfg, err := Load("")
			require.NoError(t, err)
			require.Equal(t, 3*time.Second, cfg.DB.TxnTimeout)
			require.Equal(t, time.Second, cfg.Cache.OpTimeout)
			require.Equal(t, 1, strings.Count(output.String(), "level=WARN"))
			require.Contains(t, output.String(), "db.txn_timeout")
			require.Contains(t, output.String(), "cache.op_timeout")
			if value != "" {
				require.NotContains(t, output.String(), value)
			}
		})
	}
}

func TestInvalidFileTimeoutFallsBack(t *testing.T) {
	for _, key := range []string{"CH_DB_TXN_TIMEOUT", "CH_CACHE_OP_TIMEOUT"} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("db:\n  txn_timeout: invalid\ncache:\n  op_timeout: 2s\n"), 0o600))
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, 3*time.Second, cfg.DB.TxnTimeout)
	require.Equal(t, 2*time.Second, cfg.Cache.OpTimeout)
}

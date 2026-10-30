package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedisPoolConfigurationPriority(t *testing.T) {
	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, 10, cfg.Redis.PoolSize)
	require.Equal(t, 1, cfg.Redis.MinIdleConns)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("redis:\n  pool_size: 8\n  min_idle_conns: 2\n"), 0o600))
	cfg, err = Load(path)
	require.NoError(t, err)
	require.Equal(t, 8, cfg.Redis.PoolSize)
	require.Equal(t, 2, cfg.Redis.MinIdleConns)
	t.Setenv("CH_REDIS_POOL_SIZE", "6")
	t.Setenv("CH_REDIS_MIN_IDLE_CONNS", "3")
	cfg, err = Load(path)
	require.NoError(t, err)
	require.Equal(t, 6, cfg.Redis.PoolSize)
	require.Equal(t, 3, cfg.Redis.MinIdleConns)
}

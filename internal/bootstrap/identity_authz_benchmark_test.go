//go:build authzbench

package bootstrap

import (
	"os"
	"strconv"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/stretchr/testify/require"
)

func TestAuthzIntegration2000UserBenchmark(t *testing.T) {
	// An explicit address selects real Redis; keys use unique user IDs and are individually removed.
	address := os.Getenv("TEST_REDIS_ADDR")
	require.NotEmpty(t, address, "set TEST_REDIS_ADDR to run the separate real-Redis benchmark")
	database, err := strconv.Atoi(os.Getenv("TEST_REDIS_DB"))
	require.NoError(t, err)
	f := newAdminFixture(t)
	backend, err := cache.Open(f.ctx, config.Redis{Addr: address, Password: os.Getenv("TEST_REDIS_PASSWORD"), DB: database, PoolSize: 32, MinIdleConns: 2})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, backend.Close()) })
	runAuthzLatency(t, f, backend, 2000)
}

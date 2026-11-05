package testenv

import (
	"os"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
)

// Timeouts supplies larger integration budgets only when a test database exists.
// Explicit process overrides take precedence; production defaults remain intact.
func Timeouts(t testing.TB) *config.Config {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load test timeout configuration: %v", err)
	}
	if Lookup("TEST_DATABASE_URL") != "" {
		if _, exists := os.LookupEnv("CH_DB_TXN_TIMEOUT"); !exists {
			cfg.DB.TxnTimeout = 15 * time.Second
		}
		if _, exists := os.LookupEnv("CH_CACHE_OP_TIMEOUT"); !exists {
			cfg.Cache.OpTimeout = 5 * time.Second
		}
	}
	return cfg
}

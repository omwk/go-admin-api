package config

import (
	"log/slog"
	"time"

	"github.com/spf13/viper"
)

// DefaultTxnTimeout preserves the production database query and transaction budget.
const DefaultTxnTimeout = 3 * time.Second

// DefaultCacheOpTimeout preserves the production Redis operation budget.
const DefaultCacheOpTimeout = time.Second

// Cache contains the shared Redis operation budget.
type Cache struct {
	OpTimeout time.Duration `mapstructure:"op_timeout"`
}

// TransactionTimeout also supplies the default for directly constructed settings.
func (c DB) TransactionTimeout() time.Duration {
	if c.TxnTimeout <= 0 {
		return DefaultTxnTimeout
	}
	return c.TxnTimeout
}

// OperationTimeout also supplies the default for directly constructed settings.
func (c Cache) OperationTimeout() time.Duration {
	if c.OpTimeout <= 0 {
		return DefaultCacheOpTimeout
	}
	return c.OpTimeout
}

func normalizeTimeouts(v *viper.Viper) {
	var invalid []string
	for _, setting := range []struct {
		key      string
		fallback time.Duration
	}{
		{"db.txn_timeout", DefaultTxnTimeout},
		{"cache.op_timeout", DefaultCacheOpTimeout},
	} {
		value, err := time.ParseDuration(v.GetString(setting.key))
		if err != nil || value <= 0 {
			invalid = append(invalid, setting.key)
			value = setting.fallback
		}
		v.Set(setting.key, value)
	}
	if len(invalid) > 0 {
		slog.Warn("invalid timeout configuration; using defaults", "module", "config", "action", "load", "trace_id", "", "keys", invalid)
	}
}

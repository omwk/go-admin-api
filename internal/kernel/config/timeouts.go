package config

import (
	"log/slog"
	"time"

	"github.com/spf13/viper"
)

// DefaultTxnTimeout preserves the production database query and transaction budget.
const DefaultTxnTimeout = 3 * time.Second

// DefaultMigrateTimeout preserves the production budget for a complete migration run.
const DefaultMigrateTimeout = 30 * time.Second

// DefaultCacheOpTimeout preserves the production Redis operation budget.
const DefaultCacheOpTimeout = time.Second

// DefaultReplayBatchTimeout covers twice the measured single-batch P99 (D1).
const DefaultReplayBatchTimeout = 2 * time.Second

// Cache contains the shared Redis operation budget.
type Cache struct {
	OpTimeout time.Duration `mapstructure:"op_timeout"`
}

// Sync contains the bounded replay scheduling slice shared with its Redis lease.
type Sync struct {
	ReplayBatchTimeout time.Duration `mapstructure:"replay_batch_timeout"`
}

// ReplayBatchBudget also supplies the default for directly constructed settings.
func (c Sync) ReplayBatchBudget() time.Duration {
	if c.ReplayBatchTimeout <= 0 {
		return DefaultReplayBatchTimeout
	}
	return c.ReplayBatchTimeout
}

// TransactionTimeout also supplies the default for directly constructed settings.
func (c DB) TransactionTimeout() time.Duration {
	if c.TxnTimeout <= 0 {
		return DefaultTxnTimeout
	}
	return c.TxnTimeout
}

// MigrationTimeout supplies an independent budget for schema migrations.
func (c DB) MigrationTimeout() time.Duration {
	if c.MigrateTimeout <= 0 {
		return DefaultMigrateTimeout
	}
	return c.MigrateTimeout
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
		{"db.migrate_timeout", DefaultMigrateTimeout},
		{"cache.op_timeout", DefaultCacheOpTimeout},
		{"sync.replay_batch_timeout", DefaultReplayBatchTimeout},
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

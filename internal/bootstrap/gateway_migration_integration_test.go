package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGatewayMigrationIntegrationUpDownUp(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	state := migrationAt(ctx, t, database, 17)
	require.EqualValues(t, 17, state.Version)
	state, err := database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 16, state.Version)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var absent bool
	require.NoError(t, q.Raw("SELECT to_regclass('api_client') IS NULL AND to_regclass('api_client_secret') IS NULL AND to_regclass('api_route') IS NULL AND to_regclass('api_client_route') IS NULL AND to_regclass('api_client_resource_grant') IS NULL AND to_regclass('api_call_log') IS NULL").Scan(&absent).Error)
	require.True(t, absent)
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, latestBootstrapMigration(t), state.Version)
	require.False(t, state.Dirty)
	t.Logf("migration 17 -> 16 -> %d; dirty=false", state.Version)
}

func TestGatewayMigrationIntegrationNoPlaintextAndMonthlyLog(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var plaintextColumns int64
	require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='api_client_secret' AND column_name IN ('secret','secret_hash','plaintext')").Scan(&plaintextColumns).Error)
	require.Zero(t, plaintextColumns)
	var partitioned bool
	require.NoError(t, q.Raw("SELECT relkind='p' FROM pg_class WHERE oid='api_call_log'::regclass").Scan(&partitioned).Error)
	require.True(t, partitioned)
	var partitions int64
	require.NoError(t, q.Raw("SELECT count(*) FROM pg_inherits WHERE inhparent='api_call_log'::regclass").Scan(&partitions).Error)
	require.EqualValues(t, 4, partitions)
}

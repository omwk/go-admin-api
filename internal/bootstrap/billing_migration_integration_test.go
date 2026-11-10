package bootstrap

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestBillingMigrationIntegrationUpDownUp(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	state := migrationAt(ctx, t, database, 19)
	require.EqualValues(t, 19, state.Version)
	state, err := database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 18, state.Version)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var absent bool
	require.NoError(t, q.Raw("SELECT to_regclass('wallet') IS NULL AND to_regclass('wallet_txn') IS NULL AND to_regclass('call_charge') IS NULL AND to_regclass('recharge_code') IS NULL").Scan(&absent).Error)
	require.True(t, absent)
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, latestBootstrapMigration(t), state.Version)
	require.False(t, state.Dirty)
	t.Log("migration 19 -> 18 -> 19; dirty=false")
}

func TestBillingMigrationIntegrationAppendOnlyAndNonnegative(t *testing.T) {
	f := newBillingFixture(t, 100)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, sql := range []string{"UPDATE wallet_txn SET amount_cents=0", "DELETE FROM wallet_txn", "TRUNCATE wallet_txn"} {
		var pgErr *pgconn.PgError
		require.ErrorAs(t, q.Exec(sql).Error, &pgErr)
		require.Equal(t, "55000", pgErr.Code)
	}
	for _, sql := range []string{"UPDATE wallet SET balance_cents=-1", "UPDATE wallet SET frozen_cents=-1", "UPDATE wallet SET frozen_cents=101"} {
		var pgErr *pgconn.PgError
		require.ErrorAs(t, q.Exec(sql).Error, &pgErr)
		require.Equal(t, "23514", pgErr.Code)
	}
	f.assertFunds(t, 100, 0)
}

package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCredentialRefreshMigrationIntegrationFifteenFourteenFifteen(t *testing.T) {
	// REQ-SHP-008: reversible schema, with historical credential fields preserved.
	database, ctx := isolatedIdentityDB(t)
	state := migrationAt(ctx, t, database, 15)
	require.EqualValues(t, 15, state.Version)
	state, err := database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 14, state.Version)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var absent bool
	require.NoError(t, q.Raw(`SELECT to_regclass('credential_refresh_run') IS NULL
        AND to_regclass('credential_refresh_stage') IS NULL AND to_regclass('credential_refresh_reconciliation') IS NULL`).Scan(&absent).Error)
	require.True(t, absent)
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, 15, state.Version)
	require.False(t, state.Dirty)
	t.Log("migration 15 -> 14 -> 15; dirty=false")
}

func TestCredentialRefreshMigrationIntegrationConstraints(t *testing.T) {
	f := newCredentialRefreshFixture(t)
	f.prepare(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, statement := range []string{
		"UPDATE shop_credential SET retry_budget=-1",
		"UPDATE shop_credential SET retry_budget=6",
		"UPDATE shop_credential SET refresh_generation=-1",
		"UPDATE shop_credential SET status='expired'",
		"UPDATE credential_refresh_run SET attempt=6",
		"UPDATE credential_refresh_run SET phase='published'",
		"UPDATE credential_refresh_run SET tenant_id=2",
	} {
		require.Error(t, q.Exec(statement).Error, statement)
	}
	var stages int64
	require.NoError(t, q.Raw(`SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema()
        AND table_name='credential_refresh_stage' AND column_name IN ('values','response','plain_text')`).Scan(&stages).Error)
	require.Zero(t, stages)
}

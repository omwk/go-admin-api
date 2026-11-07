package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrderMigrationIntegrationTenNineTenAndStructuralInvariants(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	state, err := database.Migrate(ctx, "version")
	require.NoError(t, err)
	require.EqualValues(t, 10, state.Version)
	require.False(t, state.Dirty)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var foreignKeys, statusColumns, cleartextColumns int64
	require.NoError(t, q.Raw(`SELECT count(*) FROM pg_constraint WHERE contype='f' AND conrelid IN
		('sales_order'::regclass,'order_item'::regclass,'order_address'::regclass,'order_status_history'::regclass,'order_sync_pending'::regclass)`).Scan(&foreignKeys).Error)
	require.Zero(t, foreignKeys)
	require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='sales_order' AND column_name='status'").Scan(&statusColumns).Error)
	require.Zero(t, statusColumns, "display status is only a projection")
	require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='order_address' AND column_name IN ('receiver_name','phone','address','postal_code','region_hint')").Scan(&cleartextColumns).Error)
	require.Zero(t, cleartextColumns)
	state, err = database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 9, state.Version)
	for _, table := range []string{"sales_order", "order_item", "order_address", "order_status_history", "order_sync_pending"} {
		var absent bool
		require.NoError(t, q.Raw("SELECT to_regclass(?) IS NULL", table).Scan(&absent).Error)
		require.True(t, absent)
	}
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, 10, state.Version)
	require.False(t, state.Dirty)
	t.Log("migration 10 -> 9 -> 10; five tables restored; no business foreign keys or stored display status")
}

package bootstrap

import (
	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOrderVisibilityMigrationIntegrationElevenTenElevenPreservesHistory(t *testing.T) {
	f := newOrderFixture(t)
	result, err := f.apply(f.snapshot(t, "visibility-migration"))
	require.NoError(t, err)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	history := order.StatusHistory{TenantID: 1, OrderID: result.OrderID, Dimension: "visibility", FromStatus: "active", ToStatus: "vanished", EventKind: "snapshot", Reason: "platform_deleted", EventKey: "migration-evidence", ObservedAt: f.snapshot(t, "observation").Source.FetchedAt, Evidence: []byte(`{"source":{"raw_id":1}}`)}
	require.NoError(t, q.Create(&history).Error)
	state, err := f.database.Migrate(f.ctx, "version")
	require.NoError(t, err)
	require.EqualValues(t, 12, state.Version)
	state, err = f.database.Migrate(f.ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 11, state.Version)
	require.False(t, state.Dirty)
	state, err = f.database.Migrate(f.ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 10, state.Version)
	require.False(t, state.Dirty)
	var count int64
	require.NoError(t, q.Model(&order.StatusHistory{}).Where("id=? AND dimension='visibility'", history.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='sales_order' AND column_name IN ('vanished_at','vanish_reason')").Scan(&count).Error)
	require.Zero(t, count)
	state, err = f.database.Migrate(f.ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, 12, state.Version)
	require.False(t, state.Dirty)
	require.NoError(t, q.Raw("SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname='idx_order_vanished' AND indexdef LIKE '%WHERE (vanished_at IS NOT NULL)%'").Scan(&count).Error)
	require.EqualValues(t, 1, count)
	t.Log("migration 12 -> 11 -> 10 -> 12; dirty=false; visibility history retained")
}

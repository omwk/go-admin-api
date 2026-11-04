package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func TestCatalogInventoryIntegrationSyncSnapshots(t *testing.T) {
	// REQ-PRD-004: page writes include one snapshot for every accepted SKU.
	f := newSyncFixture(t, syncDataset(t, 3))
	started := time.Now().UTC()
	f.execute(t, f.run(t))
	rawCount(f.ctx, t, f.database, "listing_sku", 3)
	rawCount(f.ctx, t, f.database, "inventory_snapshot", 3)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []catalog.InventorySnapshot
	require.NoError(t, q.Select("id,tenant_id,shop_id,listing_sku_id,available,reserved,synced_at").Find(&rows).Error)
	for _, row := range rows {
		require.Equal(t, 10, row.Available)
		require.Zero(t, row.Reserved)
		require.Equal(t, f.shop.ID, row.ShopID)
		require.False(t, row.SyncedAt.Before(started))
	}
	old := time.Now().UTC().Add(-24 * time.Hour)
	mapped := ingestion.Mapped{TenantID: 1, ShopID: f.shop.ID, PlatformID: f.shop.PlatformID,
		ExternalID: "old-raw", SourceFetchedAt: old, SourceOrder: 1, MappingChecksum: "inventory-test",
		Values: map[string]any{"currency": "CNY"}, Collections: map[string][]map[string]any{
			"skus": {{"external_sku_id": "old-sku", "stock": 7}},
		}}
	write := func() error {
		return f.database.Transaction(f.ctx, func(ctx context.Context) error {
			tx, stop := f.database.WithContext(ctx)
			defer stop()
			_, err := (catalog.Upserter{}).Upsert(ctx, tx, ingestion.ResourceProduct, []ingestion.Mapped{mapped})
			return err
		})
	}
	require.NoError(t, write())
	require.NoError(t, write()) // Re-delivery of the same source is skipped as a group.
	rawCount(f.ctx, t, f.database, "inventory_snapshot", 4)
	var newest catalog.InventorySnapshot
	require.NoError(t, q.Select("id,available,synced_at").Order("id DESC").Take(&newest).Error)
	require.Equal(t, 7, newest.Available)
	require.False(t, newest.SyncedAt.Before(started))
	require.True(t, newest.SyncedAt.After(old))
	mapped.SourceOrder++
	mapped.Collections["skus"][0]["stock"] = 9
	mapped.Collections["skus"][0]["ext"] = map[string]any{"reserved": 2}
	require.NoError(t, write())
	rawCount(f.ctx, t, f.database, "listing_sku", 4)
	rawCount(f.ctx, t, f.database, "inventory_snapshot", 5)
	var updated catalog.InventorySnapshot
	require.NoError(t, q.Select("id,listing_sku_id,available,reserved,synced_at").Order("id DESC").Take(&updated).Error)
	var sku catalog.ListingSKU
	require.NoError(t, q.Select("id,stock").Where("id=?", updated.ListingSKUID).Take(&sku).Error)
	require.Equal(t, 9, updated.Available)
	require.Equal(t, sku.Stock, updated.Available)
	require.Equal(t, 2, updated.Reserved)
}

func TestCatalogInventoryIntegrationRetentionAndScopedReads(t *testing.T) {
	// REQ-PRD-004: strict 30-day cutoff preserves the boundary and recent history.
	f := newSyncFixture(t, syncDataset(t, 3))
	f.execute(t, f.run(t))
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var ids []int64
	require.NoError(t, q.Table("inventory_snapshot").Order("id").Pluck("id", &ids).Error)
	require.NoError(t, q.Table("inventory_snapshot").Where("id=?", ids[0]).Update("synced_at", now.Add(-31*24*time.Hour)).Error)
	require.NoError(t, q.Table("inventory_snapshot").Where("id=?", ids[1]).Update("synced_at", now.Add(-30*24*time.Hour)).Error)
	cleanup := inventoryDailyCleanup(f.database)
	require.NoError(t, cleanup(f.ctx, now))
	rawCount(f.ctx, t, f.database, "inventory_snapshot", 2)
	require.NoError(t, cleanup(f.ctx, now.Add(time.Hour)))
	rawCount(f.ctx, t, f.database, "inventory_snapshot", 2) // Already cleaned this UTC day.
	store := NewInventoryStore(f.database)
	rows, total, err := store.ListInventories(f.ctx, 1, catalog.InventoryQuery{Page: 1, PageSize: 1})
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, rows, 1)
	require.Equal(t, ids[2], rows[0].ID)
	rows, total, err = store.ListInventories(f.ctx, 1, catalog.InventoryQuery{Page: 1, PageSize: 20, ListingSKUID: rows[0].ListingSKUID})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	_, _, err = store.ListInventories(f.ctx, 2, catalog.InventoryQuery{Page: 1, PageSize: 20})
	require.Error(t, err)
	denied := identityscope.WithAccess(f.ctx, identityscope.Access{Tenant: 1, User: f.actor.UserID + 100, Roles: []identityscope.Role{{Kind: "self"}}})
	rows, total, err = store.ListInventories(denied, 1, catalog.InventoryQuery{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, rows)
	require.NoError(t, cleanup(f.ctx, now.Add(24*time.Hour)))
	rawCount(f.ctx, t, f.database, "inventory_snapshot", 1)
}

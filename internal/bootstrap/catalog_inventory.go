package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
)

type inventoryStore struct{ *categoryManagementStore }

// NewInventoryStore constructs the scoped platform inventory history adapter.
func NewInventoryStore(database *db.DB) catalog.InventoryStore {
	return &inventoryStore{&categoryManagementStore{database: database}}
}

func (s *inventoryStore) ListInventories(ctx context.Context, tenant int64, query catalog.InventoryQuery) ([]catalog.InventorySnapshot, int64, error) {
	if err := catalogQuery("Catalog.ListInventories"); err != nil {
		return nil, 0, err
	}
	human, err := identityscope.From(ctx)
	if err != nil || human.Tenant != tenant {
		return nil, 0, identityscope.Denied()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	shops, err := shopScope(ctx, &shopmodel.Shop{}, q.Table("shop").Select("shop.id"))
	if err != nil {
		return nil, 0, err
	}
	base := q.Model(&catalog.InventorySnapshot{}).Where("tenant_id=? AND shop_id IN (?)", tenant, shops)
	if query.ShopID > 0 {
		base = base.Where("shop_id=?", query.ShopID)
	}
	if query.ListingSKUID > 0 {
		base = base.Where("listing_sku_id=?", query.ListingSKUID)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []catalog.InventorySnapshot{}
	err = base.Select("id,tenant_id,shop_id,listing_sku_id,available,reserved,synced_at").
		Order("synced_at DESC,id DESC").Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&rows).Error
	return rows, total, err
}

func cleanupInventorySnapshots(ctx context.Context, database *db.DB, now time.Time) error {
	if err := catalogQuery("Catalog.CleanupInventorySnapshots"); err != nil {
		return err
	}
	q, cancel := database.WithContext(ctx)
	defer cancel()
	return q.Where("synced_at < ?", now.UTC().Add(-30*24*time.Hour)).Delete(&catalog.InventorySnapshot{}).Error
}

// inventoryDailyCleanup runs at startup and once per UTC day under the scheduler lease.
func inventoryDailyCleanup(database *db.DB) func(context.Context, time.Time) error {
	var completedDay time.Time
	return func(ctx context.Context, now time.Time) error {
		day := now.UTC().Truncate(24 * time.Hour)
		if day.Equal(completedDay) {
			return nil
		}
		if err := cleanupInventorySnapshots(ctx, database, now); err != nil {
			return err
		}
		completedDay = day
		return nil
	}
}

func inventorySchedule(database *db.DB, schedule func(context.Context, time.Time) error) func(context.Context) error {
	cleanup := inventoryDailyCleanup(database)
	return func(ctx context.Context) error {
		now := time.Now().UTC()
		return errors.Join(schedule(ctx, now), cleanup(ctx, now))
	}
}

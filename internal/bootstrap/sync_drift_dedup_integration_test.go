package bootstrap

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

func TestSyncDriftIntegrationRollingTenantDedupAcrossShops(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	warmSchema(t, f)
	f.platform.Fault = "drift"
	completeCycle(t, f)
	firstShop := f.shop.ID
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	other := f.shop
	other.ID = 0
	other.ExternalShopID = "other-drift-shop"
	other.Name = "other drift shop"
	require.NoError(t, q.Create(&other).Error)
	f.shop = other
	f.create(t, "api_key")
	var job struct{ Options []byte }
	require.NoError(t, q.Table("sync_job").Select("options").Where("id=?", f.jobID).Take(&job).Error)
	row := map[string]any{"tenant_id": 1, "shop_id": other.ID, "resource_type": "product", "cron": "0 0 1 1 *", "enabled": true, "options": string(job.Options)}
	require.NoError(t, q.Table("sync_job").Clauses(clause.Returning{}).Create(row).Error)
	f.jobID = row["id"].(int64)
	completeCycle(t, f)
	var events []repo.DriftEvent
	require.NoError(t, q.Table("schema_drift_event").Find(&events).Error)
	require.Len(t, events, 1)
	require.Equal(t, firstShop, events[0].ShopID)
	// The window rolls from detected_at; it is not a calendar-day bucket.
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE schema_drift_event SET detected_at=now()-interval '25 hours'"))
	completeCycle(t, f)
	events = nil
	require.NoError(t, q.Table("schema_drift_event").Order("id").Find(&events).Error)
	require.Len(t, events, 2)
	require.Equal(t, events[0].BaselineID, events[1].BaselineID)
	require.Equal(t, events[0].Kind, events[1].Kind)
	require.Equal(t, events[0].Path, events[1].Path)
	require.Equal(t, other.ID, events[1].ShopID)
}

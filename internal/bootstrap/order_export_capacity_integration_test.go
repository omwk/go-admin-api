package bootstrap

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
)

func TestOrderExportIntegrationHundredThousandRows(t *testing.T) {
	// REQ-ORD-007: PR CI uses 1000 rows; local acceptance selects 100000.
	count := 1000
	if os.Getenv("ORDER_EXPORT_CAPACITY_FULL") == "1" {
		count = 100000
	}
	f := newOrderExportFixture(t)
	f.seedExportOrders(t, count)
	job := f.create(t)
	started := time.Now()
	require.NoError(t, f.files.Execute(f.ctx, 1, job.ID))
	elapsed := time.Since(started)
	require.Less(t, elapsed, 180*time.Second)
	result, err := f.store.ReadExport(f.ctx, 1, f.actor.UserID, job.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", result.Status)
	require.EqualValues(t, count, result.RowTotal)
	require.EqualValues(t, count, result.RowCount)
	records := f.csv(t, job.ID)
	require.Len(t, records, count+1)
	require.Equal(t, order.ExportCSVHeader(), records[0])
	seen := make(map[string]bool, count)
	for _, row := range records[1:] {
		require.False(t, seen[row[0]])
		seen[row[0]] = true
	}
	t.Logf("order export rows=%d total=%s CSV_records=%d (full size via ORDER_EXPORT_CAPACITY_FULL=1)", count, elapsed, len(records)-1)
}

func (f *orderExportFixture) seedExportOrders(t *testing.T, count int) {
	t.Helper()
	now := time.Now().UTC()
	rows := make([]order.Order, min(500, count))
	for start := 0; start < count; start += 500 {
		rows = rows[:min(500, count-start)]
		for i := range rows {
			rows[i] = order.Order{TenantID: 1, ShopID: f.shop.ID, PlatformID: f.shop.PlatformID, ExternalOrderID: fmt.Sprintf("export-capacity-%09d", start+i),
				States: order.States{Lifecycle: "shipped", Fulfillment: "shipped", Payment: "paid", AfterSales: "none", Cancel: "none"}, Currency: "CNY",
				Money: order.Money{GoodsAmountCents: 100, TotalAmountCents: 100, PaidAmountCents: 100}, PaymentSettled: true, RawID: 1,
				SourceFetchedAt: now, MappingChecksum: "export-capacity-map", ContentHash: "export-capacity-content", SyncedAt: now, CreatedAt: now, UpdatedAt: now, PlacedAt: &now}
		}
		q, cancel := f.database.WithContext(f.ctx)
		err := q.CreateInBatches(rows, 500).Error
		cancel()
		require.NoError(t, err)
	}
}

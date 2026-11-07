package bootstrap

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
)

func TestOrderQueryIntegrationNonPIIListP95(t *testing.T) {
	// REQ-ORD-005: CI uses 1000 rows; local/nightly can select the full 100000.
	count := 1000
	if value := os.Getenv("ORDER_PERF_ROWS"); value != "" {
		var err error
		count, err = strconv.Atoi(value)
		require.NoError(t, err)
		require.GreaterOrEqual(t, count, 20)
		require.LessOrEqual(t, count, 1000000)
	}
	f := newOrderQueryFixture(t)
	now := time.Now().UTC()
	rows := make([]order.Order, min(500, count))
	for start := 0; start < count; start += 500 {
		rows = rows[:min(500, count-start)]
		for i := range rows {
			rows[i] = order.Order{TenantID: 1, ShopID: f.shop.ID, PlatformID: f.shop.PlatformID, ExternalOrderID: fmt.Sprintf("perf-order-%09d", start+i),
				States: order.States{Lifecycle: "shipped", Fulfillment: "shipped", Payment: "paid", AfterSales: "none", Cancel: "none"}, Currency: "CNY",
				Money: order.Money{GoodsAmountCents: 100, TotalAmountCents: 100, PaidAmountCents: 100}, PaymentSettled: true, RawID: 1, SourceFetchedAt: now, MappingChecksum: "perf-map", ContentHash: "perf-content", SyncedAt: now, CreatedAt: now, UpdatedAt: now, PlacedAt: &now}
		}
		q, cancel := f.database.WithContext(f.ctx)
		err := q.CreateInBatches(rows, 500).Error
		cancel()
		require.NoError(t, err)
	}
	q, cancel := f.database.WithContext(f.ctx)
	require.NoError(t, q.Exec("ANALYZE sales_order").Error)
	cancel()
	path := fmt.Sprintf("/orders?shop_id=%d&status=shipped&min_amount_cents=100&max_amount_cents=100", f.shop.ID)
	durations := make([]time.Duration, 25)
	for i := range durations {
		started := time.Now()
		page := f.page(t, path)
		durations[i] = time.Since(started)
		require.EqualValues(t, count, page.Total)
		require.Len(t, page.List, 20)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("non-PII order list: rows=%d requests=25 P95=%s", count, durations[23])
	require.Less(t, durations[23], 400*time.Millisecond, "non-PII order list P95")
}

package bootstrap

import (
	"encoding/json"
	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestOrderSyncIntegrationPendingRetriesGateWatermark(t *testing.T) {
	// REQ-ORD-001 / REQ-SYNC-013: discovery is not detail coverage.
	f, p := newOrderSyncFixture(t, 10)
	for _, id := range []string{"order-02", "order-05", "order-08"} {
		p.failures[id] = true
	}
	run := f.run(t)
	f.execute(t, run)
	require.EqualValues(t, 7, orderTableCount(t, f, "sales_order", "true"))
	require.EqualValues(t, 3, orderTableCount(t, f, "order_sync_pending", "status='pending'"))
	require.Zero(t, orderTableCount(t, f, "sales_order", "vanished_at IS NOT NULL"))
	require.Empty(t, f.cursor(t), "details incomplete: watermark cannot move")
	current := f.readRun(t, run.ID)
	require.Equal(t, 7, current.Stats.OrderCompleted)
	require.Equal(t, 3, current.Stats.OrderPending)
	p.failures = map[string]bool{}
	q, cancel := f.database.WithContext(f.ctx)
	require.NoError(t, q.Table("order_sync_pending").Where("shop_id=?", f.shop.ID).Update("next_retry_at", time.Now().UTC().Add(-time.Second)).Error)
	cancel()
	f.execute(t, current)
	require.EqualValues(t, 10, orderTableCount(t, f, "sales_order", "true"))
	require.Zero(t, orderTableCount(t, f, "order_sync_pending", "status <> 'done'"))
	watermark, err := time.Parse(time.RFC3339Nano, f.cursor(t))
	require.NoError(t, err)
	require.WithinDuration(t, run.StartedAt, watermark, time.Microsecond)
	require.Equal(t, "success", f.readRun(t, run.ID).Status)
	t.Log("10 discovered -> 7 stored / 3 pending / 0 vanished / unchanged watermark -> 10 stored / 0 pending / watermark advanced")
}

func TestOrderSyncIntegrationEqualTimestampAcrossPagesAndAbsentNextCycle(t *testing.T) {
	f, p := newOrderSyncFixture(t, 2)
	rows := p.pages[0].Orders
	p.pages = []connector.OrderListResult{{Orders: rows[:1], HasMore: true, NextCursor: "page-1"}, {Orders: rows, NextCursor: "page-2"}}
	run := f.run(t)
	f.execute(t, run)
	require.EqualValues(t, 2, orderTableCount(t, f, "sales_order", "true"))
	require.Equal(t, 1, p.calls["order-00"])
	require.Equal(t, 1, p.calls["order-01"])
	require.Len(t, p.requests, 2)
	require.Equal(t, p.requests[0].From, p.requests[1].From)
	require.Equal(t, p.requests[0].Until, p.requests[1].Until)
	before := f.cursor(t)
	p.pages = nil
	next := f.run(t)
	f.execute(t, next)
	require.EqualValues(t, 2, orderTableCount(t, f, "sales_order", "vanished_at IS NULL"))
	require.Equal(t, "success", f.readRun(t, next.ID).Status)
	watermark, err := time.Parse(time.RFC3339Nano, before)
	require.NoError(t, err)
	require.Equal(t, watermark.Add(-15*time.Minute), p.requests[2].From)
}

func TestOrderSyncIntegrationListFailureAndRawGatePreserveCheckpoint(t *testing.T) {
	f, p := newOrderSyncFixture(t, 1)
	p.listError = true
	run := f.run(t)
	f.execute(t, run)
	require.Empty(t, f.cursor(t))
	require.Zero(t, orderTableCount(t, f, "sales_order", "true"))
	current := f.readRun(t, run.ID)
	require.True(t, current.Stats.Pending)
	p.listError = false
	p.pii = true
	// M5c: fail closed if an acquisition loses its required raw protector.
	f.engineStore.rawProtector = nil
	q, cancel := f.database.WithContext(f.ctx)
	require.NoError(t, q.Table("sync_run").Where("id=?", run.ID).Update("ready_at", time.Now().UTC().Add(-time.Second)).Error)
	cancel()
	require.Error(t, f.engine.Execute(f.ctx, f.jobID, run.ID))
	require.Zero(t, orderTableCount(t, f, "raw_payload", "true"))
	q, cancel = f.database.WithContext(f.ctx)
	defer cancel()
	var row struct{ CursorMeta json.RawMessage }
	require.NoError(t, q.Table("sync_cursor").Select("cursor_meta").Where("shop_id=? AND resource_type='order'", f.shop.ID).Take(&row).Error)
	require.Contains(t, string(row.CursorMeta), "A+B")
}

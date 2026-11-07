package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

func TestOrderSyncIntegrationIncompleteOrOlderDetailRemainsPending(t *testing.T) {
	for _, scenario := range []string{"missing_items", "older_revision"} {
		t.Run(scenario, func(t *testing.T) {
			f, p := newOrderSyncFixture(t, 1)
			good := p.details["order-00"]
			if scenario == "missing_items" {
				var payload map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(good, &payload))
				delete(payload, "items")
				var err error
				p.details["order-00"], err = json.Marshal(payload)
				require.NoError(t, err)
			} else {
				p.details["order-00"] = orderDetailPayload(t, "order-00", 0)
			}
			run := f.run(t)
			f.execute(t, run)
			require.Empty(t, f.cursor(t))
			require.Zero(t, orderTableCount(t, f, "sales_order", "true"))
			require.EqualValues(t, 1, orderTableCount(t, f, "order_sync_pending", "status='pending'"))
			p.details["order-00"] = good
			q, cancel := f.database.WithContext(f.ctx)
			require.NoError(t, q.Table("order_sync_pending").Where("shop_id=?", f.shop.ID).Update("next_retry_at", time.Now().UTC().Add(-time.Second)).Error)
			cancel()
			f.execute(t, f.readRun(t, run.ID))
			require.EqualValues(t, 1, orderTableCount(t, f, "sales_order", "true"))
			require.NotEmpty(t, f.cursor(t))
		})
	}
}

func TestOrderSyncIntegrationAggregateAndPendingCommitTogether(t *testing.T) {
	f, _ := newOrderSyncFixture(t, 1)
	run := f.run(t)
	require.NoError(t, f.database.Exec(f.ctx, "ALTER TABLE order_sync_pending ADD CONSTRAINT injected_pending_failure CHECK (status <> 'done') NOT VALID"))
	require.ErrorContains(t, f.engine.Execute(f.ctx, f.jobID, run.ID), "injected_pending_failure")
	require.NoError(t, f.database.Exec(f.ctx, "ALTER TABLE order_sync_pending DROP CONSTRAINT injected_pending_failure"))
	require.Zero(t, orderTableCount(t, f, "sales_order", "true"))
	require.EqualValues(t, 1, orderTableCount(t, f, "raw_payload", "true"))
	require.EqualValues(t, 1, orderTableCount(t, f, "order_sync_pending", "status='pending'"))
	require.Empty(t, f.cursor(t))
	f.execute(t, f.readRun(t, run.ID))
	require.EqualValues(t, 1, orderTableCount(t, f, "sales_order", "true"))
	require.NotEmpty(t, f.cursor(t))
}

func TestOrderSyncIntegrationAuthorizationRecheckedAfterList(t *testing.T) {
	f, p := newOrderSyncFixture(t, 1)
	p.afterList = func() {
		q, cancel := f.database.WithContext(f.ctx)
		defer cancel()
		require.NoError(t, q.Table("sys_user").Where("id=?", f.actor.UserID).Update("status", "disabled").Error)
	}
	run := f.run(t)
	require.Error(t, f.engine.Execute(f.ctx, f.jobID, run.ID))
	require.Zero(t, orderTableCount(t, f, "raw_payload", "true"))
	require.Zero(t, orderTableCount(t, f, "sales_order", "true"))
	require.Zero(t, p.calls["order-00"])
	require.Empty(t, f.cursor(t))
}

func TestOrderSyncIntegrationEachCallConsumesSharedRateBudget(t *testing.T) {
	f, p := newOrderSyncFixture(t, 10)
	p.beforeCall = nil
	p.afterList = func() {
		require.NoError(t, f.redis.cache.DeferBucket(f.ctx, fmt.Sprintf("sync:bucket:%d:%d", 1, f.shop.ID), 10*time.Second))
	}
	run := f.run(t)
	f.execute(t, run)
	var calls int
	for _, count := range p.calls {
		calls += count
	}
	require.Zero(t, calls, "a deferred shared bucket prevents every detail network call")
	require.EqualValues(t, 10, orderTableCount(t, f, "order_sync_pending", "status='pending'"))
	require.Empty(t, f.cursor(t))
}

func TestOrderSyncIntegrationWindowResumesAfterRuntimeRestart(t *testing.T) {
	f, p := newOrderSyncFixture(t, 2)
	rows := p.pages[0].Orders
	p.pages = []connector.OrderListResult{{Orders: rows[:1], HasMore: true, NextCursor: "page-1"}, {Orders: rows[1:]}}
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sync_job SET options=jsonb_set(options,'{max_pages_per_run}','1') WHERE id=?", f.jobID))
	run := f.run(t)
	f.execute(t, run)
	require.Empty(t, f.cursor(t))
	require.EqualValues(t, 1, orderTableCount(t, f, "sales_order", "true"))
	registry := connector.NewRegistry()
	require.NoError(t, registry.Register(p))
	var err error
	f.engine, f.engineStore, err = buildSyncRuntime(f.database, f.redis.cache, f.credentialFixture.service, registry, testenv.Timeouts(t).Sync, f.sealer)
	require.NoError(t, err)
	f.execute(t, f.readRun(t, run.ID))
	require.EqualValues(t, 2, orderTableCount(t, f, "sales_order", "true"))
	require.Equal(t, p.requests[0].From, p.requests[1].From)
	require.Equal(t, p.requests[0].Until, p.requests[1].Until)
	require.Equal(t, "page-1", p.requests[1].Cursor)
	require.Equal(t, 1, p.calls["order-00"])
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sync_job SET options=jsonb_set(options,'{order_lookback_seconds}','90') WHERE id=?", f.jobID))
	p.pages = nil
	f.execute(t, f.run(t))
	require.WithinDuration(t, run.StartedAt.Add(-90*time.Second), p.requests[2].From, time.Microsecond)
}

func TestOrderSyncIntegrationExpiredSchedulingFenceRejectsListCommit(t *testing.T) {
	f, p := newOrderSyncFixture(t, 1)
	run := f.run(t)
	p.afterList = func() {
		require.NoError(t, f.database.Exec(f.ctx, "UPDATE sync_run SET scheduling_generation=scheduling_generation+1 WHERE id=?", run.ID))
	}
	require.Error(t, f.engine.Execute(f.ctx, f.jobID, run.ID))
	require.Zero(t, orderTableCount(t, f, "raw_payload", "true"))
	require.Zero(t, orderTableCount(t, f, "order_sync_pending", "true"))
	require.Empty(t, f.cursor(t))
}

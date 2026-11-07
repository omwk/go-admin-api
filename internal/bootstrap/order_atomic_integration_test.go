package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

func (f *orderFixture) applyCheckpoint(s order.Snapshot, failCheckpoint bool) error {
	return f.database.Transaction(f.ctx, func(ctx context.Context) error {
		q, cancel := f.database.WithContext(ctx)
		defer cancel()
		var cursor string
		if err := q.Table("sync_cursor").Select("cursor_value").Where("tenant_id=? AND shop_id=? AND resource_type=?", s.TenantID, s.ShopID, "order").Clauses(clause.Locking{Strength: "UPDATE"}).Take(&cursor).Error; err != nil {
			return err
		}
		if _, err := f.upserter.Apply(ctx, q, s); err != nil {
			return err
		}
		if failCheckpoint {
			return errors.New("injected checkpoint failure")
		}
		return q.Table("sync_cursor").Where("tenant_id=? AND shop_id=? AND resource_type=?", s.TenantID, s.ShopID, "order").Update("cursor_value", "next").Error
	})
}

func TestOrderAtomicIntegrationHistoryFailureRollsBackAggregateAndCheckpoint(t *testing.T) {
	// REQ-ORD-004/007: the last aggregate stage cannot leave partial facts.
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "update"}[existing], func(t *testing.T) {
			f := newOrderFixture(t)
			first := f.snapshot(t, "atomic-instance")
			if existing {
				result, err := f.apply(first)
				require.NoError(t, err)
				require.Equal(t, "applied", result.Outcome)
			}
			require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO sync_cursor (tenant_id,shop_id,resource_type,cursor_type,cursor_value) VALUES (1,?,'order','timestamp','before')", f.shopID))
			before := f.aggregate(t)
			require.NoError(t, f.database.Exec(f.ctx, "ALTER TABLE order_status_history ADD CONSTRAINT injected_history_failure CHECK (false) NOT VALID"))
			candidate := f.snapshot(t, "atomic-instance")
			candidate.States.Lifecycle, candidate.States.Fulfillment = "completed", "delivered"
			if existing {
				candidate.ExpectedVersion = 1
			}
			require.Error(t, f.applyCheckpoint(candidate, false))
			require.Equal(t, before, f.aggregate(t))
			if !existing {
				for _, table := range []string{"sales_order", "order_item", "order_address", "order_status_history"} {
					require.JSONEq(t, `[]`, f.aggregate(t)[table])
				}
			}
		})
	}
}

func TestOrderAtomicIntegrationEveryWriteFailureRollsBack(t *testing.T) {
	for _, table := range []string{"sales_order", "order_item", "order_address"} {
		t.Run(table, func(t *testing.T) {
			f := newOrderFixture(t)
			s := f.snapshot(t, "write-failure")
			before := f.aggregate(t)
			require.NoError(t, f.database.Exec(f.ctx, "ALTER TABLE "+table+" ADD CONSTRAINT injected_failure CHECK (false) NOT VALID"))
			_, err := f.apply(s)
			require.Error(t, err)
			require.Equal(t, before, f.aggregate(t))
		})
	}
}

func TestOrderAtomicIntegrationCheckpointFailureAndSuccessfulCommit(t *testing.T) {
	f := newOrderFixture(t)
	s := f.snapshot(t, "checkpoint-instance")
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO sync_cursor (tenant_id,shop_id,resource_type,cursor_type,cursor_value) VALUES (1,?,'order','timestamp','before')", f.shopID))
	before := f.aggregate(t)
	require.Error(t, f.applyCheckpoint(s, true))
	require.Equal(t, before, f.aggregate(t))
	require.NoError(t, f.applyCheckpoint(s, false))
	after := f.aggregate(t)
	for _, table := range []string{"sales_order", "order_item", "order_address", "order_status_history", "sync_cursor"} {
		require.NotEqual(t, before[table], after[table])
	}
}

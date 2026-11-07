package bootstrap

import (
	"sync"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
)

func TestOrderConcurrencyIntegrationVersionConflictWithoutLostUpdates(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "competing-create", true: "competing-update"}[existing], func(t *testing.T) {
			f := newOrderFixture(t)
			version := int64(0)
			if existing {
				first, err := f.apply(f.snapshot(t, "concurrent"))
				require.NoError(t, err)
				version = first.Version
			}
			inputs := []order.Snapshot{f.snapshot(t, "concurrent"), f.snapshot(t, "concurrent")}
			for i := range inputs {
				inputs[i].ExpectedVersion = version
				inputs[i].OrderNo = []string{"winner-a", "winner-b"}[i]
				inputs[i].Items.Rows[0].Title = inputs[i].OrderNo
			}
			start := make(chan struct{})
			var workers sync.WaitGroup
			results, failures := make([]order.Result, 2), make([]error, 2)
			for i := range inputs {
				workers.Add(1)
				go func(index int) {
					defer workers.Done()
					<-start
					results[index], failures[index] = f.apply(inputs[index])
				}(i)
			}
			close(start)
			workers.Wait()
			winner, conflicts := -1, 0
			for i, err := range failures {
				if err == nil {
					require.Equal(t, "applied", results[i].Outcome)
					winner = i
				} else {
					require.Equal(t, 409, errs.From(err).HTTPStatus)
					require.Equal(t, 10409, errs.From(err).Code)
					conflicts++
				}
			}
			require.Equal(t, 1, conflicts)
			require.NotEqual(t, -1, winner)
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			var parents []order.Order
			require.NoError(t, q.Select("id,version,order_no,revision").Where("tenant_id=1 AND shop_id=? AND external_order_id=?", f.shopID, "concurrent").Find(&parents).Error)
			require.Len(t, parents, 1)
			require.Equal(t, version+1, parents[0].Version)
			require.Equal(t, inputs[winner].OrderNo, parents[0].OrderNo)
			require.Equal(t, inputs[winner].OrderNo, f.items(t, parents[0].ID)[0].Title)
		})
	}
}

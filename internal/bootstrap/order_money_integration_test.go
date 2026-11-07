package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
)

func TestOrderMoneyIntegrationQuarantinePreservesPlatformAmounts(t *testing.T) {
	for _, kind := range []string{"formula", "payment"} {
		t.Run(kind, func(t *testing.T) {
			f := newOrderFixture(t)
			first, err := f.apply(f.snapshot(t, "money"))
			require.NoError(t, err)
			before := f.aggregate(t)
			next := f.snapshot(t, "money")
			next.ExpectedVersion = first.Version
			if kind == "formula" {
				next.Money.TotalAmountCents += 20
				next.Money.PaidAmountCents += 20
			} else {
				next.Money.PaidAmountCents -= 20
			}
			platform := next.Money
			result, err := f.apply(next)
			require.NoError(t, err)
			require.Equal(t, "quarantined", result.Outcome)
			require.Equal(t, kind+"_delta", result.Reason)
			require.Equal(t, platform, next.Money)
			require.Equal(t, before, f.aggregate(t))
			_, err = f.apply(next)
			require.NoError(t, err)
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			var records []struct{ ReasonDetail json.RawMessage }
			require.NoError(t, q.Table("quarantine_record").Select("reason_detail").Where("tenant_id=1 AND resource_type='order'").Find(&records).Error)
			require.Len(t, records, 1)
			var detail struct {
				Money  order.Money  `json:"platform_amounts"`
				Deltas order.Deltas `json:"deltas"`
			}
			require.NoError(t, json.Unmarshal(records[0].ReasonDetail, &detail))
			require.Equal(t, platform, detail.Money)
			require.Equal(t, result.Deltas, detail.Deltas)
		})
	}
}

func TestOrderMoneyIntegrationAcceptsUnpaidPartialPaymentAndTolerance(t *testing.T) {
	for _, paid := range []int64{0, 200, 505} {
		f := newOrderFixture(t)
		s := f.snapshot(t, "not-settled")
		s.States.Payment, s.States.Lifecycle, s.PaymentSettled, s.Money.PaidAmountCents = "unpaid", "pending_payment", false, paid
		s.Money.TotalAmountCents = 507
		result, err := f.apply(s)
		require.NoError(t, err)
		require.Equal(t, "applied", result.Outcome)
		require.Equal(t, order.Deltas{Formula: 1, Payment: paid - 507}, result.Deltas)
	}
}

func TestOrderMoneyIntegrationPlatformAllocationHasPriority(t *testing.T) {
	f := newOrderFixture(t)
	s := f.snapshot(t, "platform-discounts")
	for i, value := range []int64{21, 33, 46} {
		amount := value
		s.Items.Rows[i].OrderDiscountCents = &amount
	}
	result, err := f.apply(s)
	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
	for i, row := range f.items(t, result.OrderID) {
		require.Equal(t, "platform", row.AllocationSource)
		require.EqualValues(t, []int64{21, 33, 46}[i], row.OrderDiscountCents)
	}
}

func TestOrderMoneyIntegrationCurrenciesKeepOriginalMinorUnits(t *testing.T) {
	for _, currency := range []string{"JPY", "KWD"} {
		f := newOrderFixture(t)
		s := f.snapshot(t, "minor-units")
		s.Currency = currency
		for i := range s.Items.Rows {
			s.Items.Rows[i].Currency = currency
		}
		result, err := f.apply(s)
		require.NoError(t, err)
		require.Equal(t, "applied", result.Outcome)
		q, cancel := f.database.WithContext(f.ctx)
		var row order.Order
		require.NoError(t, q.Select("currency,total_amount_cents,paid_amount_cents").Where("id=?", result.OrderID).Take(&row).Error)
		cancel()
		require.Equal(t, currency, row.Currency)
		require.EqualValues(t, 506, row.TotalAmountCents)
		require.EqualValues(t, 506, row.PaidAmountCents)
	}
}

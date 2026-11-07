package order

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrderDiscountLargestRemainder(t *testing.T) {
	// REQ-ORD-006: derived discounts conserve the platform order discount.
	rows := []DiscountWeight{{"c", 303}, {"a", 101}, {"b", 202}}
	got, err := AllocateDiscount(100, rows)
	require.NoError(t, err)
	require.Equal(t, []int64{50, 17, 33}, got)
	require.EqualValues(t, 100, got[0]+got[1]+got[2])
	got, err = AllocateDiscount(1, []DiscountWeight{{"z", 1}, {"a", 1}})
	require.NoError(t, err)
	require.Equal(t, []int64{0, 1}, got)
	got, err = AllocateDiscount(math.MaxInt64, []DiscountWeight{{"a", math.MaxInt64}})
	require.NoError(t, err)
	require.Equal(t, []int64{math.MaxInt64}, got)
}

func TestOrderDiscountRejectsInvalidWeights(t *testing.T) {
	for _, tc := range []struct {
		name     string
		discount int64
		rows     []DiscountWeight
	}{
		{"zero base", 1, []DiscountWeight{{"a", 0}}},
		{"excess", 11, []DiscountWeight{{"a", 10}}},
		{"negative", -1, []DiscountWeight{{"a", 10}}},
		{"duplicate", 1, []DiscountWeight{{"a", 10}, {"a", 10}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AllocateDiscount(tc.discount, tc.rows)
			require.Error(t, err)
		})
	}
}

func TestOrderMoneyDeltasAndOverflow(t *testing.T) {
	policy := MoneyPolicy{FormulaToleranceCents: 2, PaymentToleranceCents: 2}
	for _, tc := range []struct {
		name             string
		money            Money
		settled          bool
		formula, payment int64
		reason           string
	}{
		{"unpaid", Money{GoodsAmountCents: 100, TotalAmountCents: 100}, false, 0, -100, ""},
		{"partial payment", Money{GoodsAmountCents: 100, TotalAmountCents: 100, PaidAmountCents: 50}, false, 0, -50, ""},
		{"within threshold", Money{GoodsAmountCents: 100, TotalAmountCents: 101, PaidAmountCents: 100}, true, 1, -1, ""},
		{"formula quarantine", Money{GoodsAmountCents: 100, TotalAmountCents: 110, PaidAmountCents: 110}, true, 10, 0, "formula_delta"},
		{"payment quarantine", Money{GoodsAmountCents: 100, TotalAmountCents: 100, PaidAmountCents: 90}, true, 0, -10, "payment_delta"},
		{"overflow", Money{GoodsAmountCents: math.MaxInt64, ShippingFeeCents: 1}, false, 0, 0, "amount_overflow"},
		{"refund exceeds paid", Money{PaidAmountCents: 10, RefundedAmountCents: 11}, false, 0, 0, "invalid_amount"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delta, reason := policy.Check(tc.money, tc.settled)
			require.Equal(t, tc.reason, reason)
			require.Equal(t, tc.formula, delta.Formula)
			require.Equal(t, tc.payment, delta.Payment)
		})
	}
}

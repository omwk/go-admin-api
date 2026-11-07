package order

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrderDiscountPartialPlatformAllocationIsRejected(t *testing.T) {
	platformDiscount := int64(25)
	s := Snapshot{Currency: "CNY", Money: Money{GoodsAmountCents: 200, DiscountCents: 50}, Items: &ItemSet{Complete: true, ExpectedCount: 2, AllocationSource: "largest_remainder_v1", Rows: []ItemInput{
		{ExternalItemID: "a", Currency: "CNY", PriceCents: 100, Qty: 1, AmountCents: 100, DiscountEligible: true, RefundStatus: "none", OrderDiscountCents: &platformDiscount},
		{ExternalItemID: "b", Currency: "CNY", PriceCents: 100, Qty: 1, AmountCents: 100, DiscountEligible: true, RefundStatus: "none"},
	}}}
	_, reason := prepareItems(s)
	require.Equal(t, "invalid_discount_allocation", reason)
}

func TestOrderDiscountEligibilityAndUnknownScopeAreExplicit(t *testing.T) {
	s := Snapshot{Currency: "CNY", Money: Money{GoodsAmountCents: 200, DiscountCents: 50}, Items: &ItemSet{Complete: true, ExpectedCount: 2, AllocationSource: "largest_remainder_v1", Rows: []ItemInput{
		{ExternalItemID: "a", Currency: "CNY", PriceCents: 100, Qty: 1, AmountCents: 100, DiscountEligible: true, RefundStatus: "none"},
		{ExternalItemID: "b", Currency: "CNY", PriceCents: 100, Qty: 1, AmountCents: 100, RefundStatus: "none"},
	}}}
	rows, reason := prepareItems(s)
	require.Empty(t, reason)
	require.EqualValues(t, 50, rows[0].OrderDiscountCents)
	require.Zero(t, rows[1].OrderDiscountCents)
	s.Items.AllocationSource = "unknown"
	rows, reason = prepareItems(s)
	require.Empty(t, reason)
	for _, row := range rows {
		require.Zero(t, row.OrderDiscountCents)
		require.Equal(t, "unknown", row.AllocationSource)
	}
}

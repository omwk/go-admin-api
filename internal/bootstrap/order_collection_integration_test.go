package bootstrap

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
)

func TestOrderCollectionIntegrationOmittedAndCompleteEmpty(t *testing.T) {
	f := newOrderFixture(t)
	first, err := f.apply(f.snapshot(t, "collection"))
	require.NoError(t, err)
	require.Equal(t, "applied", first.Outcome)
	before := f.items(t, first.OrderID)
	omitted := f.snapshot(t, "collection")
	omitted.ExpectedVersion, omitted.Items, omitted.Address = first.Version, nil, nil
	second, err := f.apply(omitted)
	require.NoError(t, err)
	require.Equal(t, "applied", second.Outcome)
	require.Equal(t, before, f.items(t, first.OrderID))
	empty := f.snapshot(t, "collection")
	empty.ExpectedVersion = second.Version
	empty.Items.Rows, empty.Items.ExpectedCount, empty.Items.EmptyConfirmed = []order.ItemInput{}, 0, true
	empty.Money.GoodsAmountCents, empty.Money.DiscountCents, empty.Money.ShippingFeeCents = 0, 0, 506
	third, err := f.apply(empty)
	require.NoError(t, err)
	require.Equal(t, "applied", third.Outcome)
	require.Empty(t, f.items(t, first.OrderID))
}

func TestOrderCollectionIntegrationStableLineIdentityAndAllocation(t *testing.T) {
	f := newOrderFixture(t)
	first, err := f.apply(f.snapshot(t, "stable-lines"))
	require.NoError(t, err)
	before := f.items(t, first.OrderID)
	require.Len(t, before, 3)
	require.EqualValues(t, 100, before[0].OrderDiscountCents+before[1].OrderDiscountCents+before[2].OrderDiscountCents)
	next := f.snapshot(t, "stable-lines")
	next.ExpectedVersion = first.Version
	next.Items.Rows[0].Title = "new observed snapshot"
	next.Items.Rows[2].ExternalItemID = "line-4"
	second, err := f.apply(next)
	require.NoError(t, err)
	require.Equal(t, "applied", second.Outcome)
	after := f.items(t, first.OrderID)
	require.Equal(t, before[0].ID, after[0].ID)
	require.Equal(t, before[1].ID, after[1].ID)
	require.NotEqual(t, before[2].ID, after[2].ID)
	require.Equal(t, "new observed snapshot", after[0].Title)
	restored := f.snapshot(t, "stable-lines")
	restored.ExpectedVersion = second.Version
	third, err := f.apply(restored)
	require.NoError(t, err)
	require.Equal(t, "applied", third.Outcome)
	require.Equal(t, before[2].ID, f.items(t, first.OrderID)[2].ID)
}

func TestOrderCollectionIntegrationRejectsIncompleteAndInvalidSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		change       func(*order.Snapshot)
	}{
		{"partial", "incomplete_items", func(s *order.Snapshot) { s.Items.Complete = false }},
		{"pagination", "incomplete_items", func(s *order.Snapshot) { s.Items.HasMore = true }},
		{"count", "incomplete_items", func(s *order.Snapshot) { s.Items.ExpectedCount = 4 }},
		{"ambiguous empty", "incomplete_items", func(s *order.Snapshot) { s.Items.Rows = nil; s.Items.ExpectedCount = 0 }},
		{"duplicate identity", "invalid_item_identity", func(s *order.Snapshot) { s.Items.Rows[1].ExternalItemID = s.Items.Rows[0].ExternalItemID }},
		{"mixed currencies", "item_currency_mismatch", func(s *order.Snapshot) { s.Items.Rows[1].Currency = "JPY" }},
		{"line arithmetic", "invalid_item_amount", func(s *order.Snapshot) { s.Items.Rows[0].AmountCents++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOrderFixture(t)
			first, err := f.apply(f.snapshot(t, "invalid-lines"))
			require.NoError(t, err)
			before := f.aggregate(t)
			next := f.snapshot(t, "invalid-lines")
			next.ExpectedVersion = first.Version
			tc.change(&next)
			result, err := f.apply(next)
			require.NoError(t, err)
			require.Equal(t, "quarantined", result.Outcome)
			require.Equal(t, tc.reason, result.Reason)
			require.Equal(t, before, f.aggregate(t))
		})
	}
}

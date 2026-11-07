package order

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

func TestOrderSnapshotStatusFactsAndRollback(t *testing.T) {
	// REQ-ORD-002/004: only the observed endpoints become history.
	old := States{"pending_payment", "unshipped", "unpaid", "none", "none"}
	next := States{"completed", "delivered", "paid", "none", "none"}
	decision, err := DecideStates(old, next, ChangeSnapshot, false)
	require.NoError(t, err)
	require.Empty(t, decision.Reason)
	require.Len(t, decision.Changes, 3)
	require.Equal(t, "pending_payment", decision.Changes[0].From)
	require.Equal(t, "completed", decision.Changes[0].To)
	decision, err = DecideStates(next, States{"paid", "unshipped", "paid", "none", "none"}, ChangeSnapshot, false)
	require.NoError(t, err)
	require.Equal(t, "rollback_requires_evidence", decision.Reason)
	decision, err = DecideStates(next, old, ChangeCorrection, true)
	require.NoError(t, err)
	require.Empty(t, decision.Reason)
	_, err = DecideStates(old, next, ChangeCommand, false)
	require.Equal(t, 50301, errs.From(err).Code)
}

func TestOrderStatusProjectionKeepsOrthogonalFacts(t *testing.T) {
	for _, tc := range []struct {
		states States
		want   string
	}{
		{States{"shipped", "shipped", "partial_refund", "partial_refunded", "none"}, "shipped"},
		{States{"completed", "delivered", "paid", "refunding", "none"}, "refunding"},
		{States{"completed", "delivered", "refunded", "refunded", "none"}, "refunded"},
		{States{"paid", "unshipped", "paid", "none", "requested"}, "pending_shipment"},
		{States{"cancelled", "unshipped", "unpaid", "none", "cancelled"}, "closed"},
	} {
		require.Equal(t, tc.want, tc.states.DisplayStatus())
	}
}

func TestOrderStateDimensionsRejectRegressionAndUnknownReset(t *testing.T) {
	base := States{"completed", "delivered", "refunded", "refunded", "cancelled"}
	for _, tc := range []struct {
		name string
		next States
	}{
		{"lifecycle", States{"paid", "delivered", "refunded", "refunded", "cancelled"}},
		{"fulfillment", States{"completed", "unshipped", "refunded", "refunded", "cancelled"}},
		{"payment", States{"completed", "delivered", "paid", "refunded", "cancelled"}},
		{"after_sales", States{"completed", "delivered", "refunded", "none", "cancelled"}},
		{"cancel", States{"completed", "delivered", "refunded", "refunded", "none"}},
		{"unknown reset", States{"unknown", "delivered", "refunded", "refunded", "cancelled"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := DecideStates(base, tc.next, ChangeSnapshot, false)
			require.NoError(t, err)
			require.NotEmpty(t, decision.Reason)
			require.Len(t, decision.Changes, 1)
		})
	}
}

func TestOrderCommandSingleStepAndRefundFailure(t *testing.T) {
	base := States{"paid", "unshipped", "paid", "none", "none"}
	next := States{"shipped", "shipped", "paid", "none", "none"}
	decision, err := DecideStates(base, next, ChangeCommand, false)
	require.NoError(t, err)
	require.Empty(t, decision.Reason)
	require.Len(t, decision.Changes, 2)
	base.AfterSales, next.AfterSales = "refunding", "none"
	decision, err = DecideStates(base, next, ChangeSnapshot, false)
	require.NoError(t, err)
	require.Empty(t, decision.Reason)
	_, err = DecideStates(States{"pending_payment", "unshipped", "unpaid", "none", "none"}, States{"paid", "unshipped", "paid", "none", "none"}, ChangeCommand, false)
	require.Equal(t, 50301, errs.From(err).Code, "commands cannot fabricate payment")
}

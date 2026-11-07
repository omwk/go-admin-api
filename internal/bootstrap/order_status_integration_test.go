package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
)

func TestOrderStatusIntegrationSnapshotJumpOnlyRecordsObservedEndpoints(t *testing.T) {
	// REQ-ORD-002/004: no synthetic paid/shipped events for an observed jump.
	f := newOrderFixture(t)
	s := f.snapshot(t, "status-jump")
	s.States.Lifecycle, s.States.Payment, s.Money.PaidAmountCents, s.PaymentSettled = "pending_payment", "unpaid", 0, false
	first, err := f.apply(s)
	require.NoError(t, err)
	next := f.snapshot(t, "status-jump")
	next.ExpectedVersion = first.Version
	next.States.Lifecycle, next.States.Fulfillment = "completed", "delivered"
	second, err := f.apply(next)
	require.NoError(t, err)
	require.Equal(t, "applied", second.Outcome)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var history []order.StatusHistory
	require.NoError(t, q.Select("dimension,from_status,to_status,occurred_at,observed_at,event_kind").Where("tenant_id=1 AND order_id=? AND from_status<>''", first.OrderID).Order("id").Find(&history).Error)
	require.Len(t, history, 3)
	require.Equal(t, "pending_payment", history[0].FromStatus)
	require.Equal(t, "completed", history[0].ToStatus)
	for _, event := range history {
		require.Nil(t, event.OccurredAt)
		require.False(t, event.ObservedAt.IsZero())
		require.Equal(t, "snapshot", event.EventKind)
	}
}

func TestOrderStatusIntegrationRollbackPreservesWholeAggregateAndDeduplicatesConflict(t *testing.T) {
	f := newOrderFixture(t)
	s := f.snapshot(t, "rollback")
	s.States.Lifecycle, s.States.Fulfillment = "completed", "delivered"
	first, err := f.apply(s)
	require.NoError(t, err)
	before := f.aggregate(t)
	next := f.snapshot(t, "rollback")
	next.ExpectedVersion, next.OrderNo = first.Version, "candidate-display-number"
	result, err := f.apply(next)
	require.NoError(t, err)
	require.Equal(t, "conflict", result.Outcome)
	require.Equal(t, "rollback_requires_evidence", result.Reason)
	after := f.aggregate(t)
	for _, table := range []string{"sales_order", "order_item", "order_address"} {
		require.Equal(t, before[table], after[table])
	}
	require.NotEqual(t, before["order_status_history"], after["order_status_history"])
	_, err = f.apply(next)
	require.NoError(t, err)
	require.Equal(t, after, f.aggregate(t), "repeat rejected candidate must not append the same conflict")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var conflicts []order.StatusHistory
	require.NoError(t, q.Select("dimension,reason,evidence").Where("tenant_id=1 AND order_id=? AND event_kind='conflict'", first.OrderID).Find(&conflicts).Error)
	require.Len(t, conflicts, 2)
	require.Contains(t, string(conflicts[0].Evidence), "raw_id")
	require.Error(t, q.Model(&order.StatusHistory{}).Where("tenant_id=1 AND order_id=?", first.OrderID).Update("reason", "rewritten").Error)
	require.Error(t, q.Where("tenant_id=1 AND order_id=?", first.OrderID).Delete(&order.StatusHistory{}).Error)
}

func TestOrderStatusIntegrationCorrectionRequiresCompleteApprovalEvidence(t *testing.T) {
	f := newOrderFixture(t)
	s := f.snapshot(t, "approved-correction")
	s.States.Lifecycle, s.States.Fulfillment = "completed", "delivered"
	first, err := f.apply(s)
	require.NoError(t, err)
	next := f.snapshot(t, "approved-correction")
	next.ExpectedVersion, next.Mode = first.Version, order.ChangeCorrection
	next.Correction = &order.Correction{ApprovedBy: 1, ReasonCode: "lifecycle_reopen", AuthoritativeDetail: true}
	result, err := f.apply(next)
	require.NoError(t, err)
	require.Equal(t, "conflict", result.Outcome)
	next.Correction.PlatformSupportsCorrection = true
	result, err = f.apply(next)
	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
	require.EqualValues(t, 2, result.Version)
	// A financial correction still needs an audit event when no status changes.
	moneyOnly := f.snapshot(t, "approved-correction")
	moneyOnly.ExpectedVersion, moneyOnly.Mode = result.Version, order.ChangeCorrection
	moneyOnly.Correction = &order.Correction{ApprovedBy: 1, ReasonCode: "payment_reversal", AuthoritativeDetail: true, PlatformSupportsCorrection: true}
	moneyOnly.Items, moneyOnly.Address, moneyOnly.Money.PaidAmountCents = nil, nil, 505
	corrected, err := f.apply(moneyOnly)
	require.NoError(t, err)
	require.Equal(t, "applied", corrected.Outcome)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var event order.StatusHistory
	require.NoError(t, q.Select("dimension,reason,evidence").Where("tenant_id=1 AND order_id=? AND event_kind='correction' AND dimension='aggregate'", first.OrderID).Take(&event).Error)
	require.Equal(t, "payment_reversal", event.Reason)
	var evidence struct {
		Correction order.Correction `json:"correction"`
		Proved     bool             `json:"correction_proved"`
	}
	require.NoError(t, json.Unmarshal(event.Evidence, &evidence))
	require.Equal(t, *moneyOnly.Correction, evidence.Correction)
	require.True(t, evidence.Proved)
}

func TestOrderStatusIntegrationPartialRefundPreservesFulfillmentAndCumulativeFacts(t *testing.T) {
	f := newOrderFixture(t)
	s := f.snapshot(t, "partial-refund")
	s.States.Lifecycle, s.States.Fulfillment = "shipped", "shipped"
	first, err := f.apply(s)
	require.NoError(t, err)
	next := f.snapshot(t, "partial-refund")
	next.ExpectedVersion, next.Items, next.Address = first.Version, nil, nil
	next.States = order.States{Lifecycle: "shipped", Fulfillment: "shipped", Payment: "partial_refund", AfterSales: "partial_refunded", Cancel: "none"}
	next.Money.RefundedAmountCents = 100
	second, err := f.apply(next)
	require.NoError(t, err)
	require.Equal(t, "applied", second.Outcome)
	require.Equal(t, "shipped", next.States.DisplayStatus())
	duplicate, err := f.apply(next)
	require.NoError(t, err)
	require.Equal(t, "skipped_same", duplicate.Outcome)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row order.Order
	require.NoError(t, q.Select("refunded_amount_cents,total_amount_cents,paid_amount_cents,lifecycle_status,fulfillment_status").Where("id=?", first.OrderID).Take(&row).Error)
	require.EqualValues(t, 100, row.RefundedAmountCents)
	require.EqualValues(t, 506, row.TotalAmountCents)
	require.EqualValues(t, 506, row.PaidAmountCents)
	require.Equal(t, "shipped", row.Fulfillment)
}

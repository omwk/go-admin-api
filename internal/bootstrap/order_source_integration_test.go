package bootstrap

import (
	"context"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
)

func TestOrderSourceIntegrationSameRevisionDedupAndHigherRevisionABA(t *testing.T) {
	f := newOrderFixture(t)
	a := f.snapshot(t, "revisions")
	first, err := f.apply(a)
	require.NoError(t, err)
	before := f.aggregate(t)
	duplicate, err := f.apply(a)
	require.NoError(t, err)
	require.Equal(t, "skipped_same", duplicate.Outcome)
	require.Equal(t, before, f.aggregate(t))
	b := f.snapshot(t, "revisions")
	b.ExpectedVersion, b.OrderNo = first.Version, "second-display"
	second, err := f.apply(b)
	require.NoError(t, err)
	require.Equal(t, "applied", second.Outcome)
	// The same protected raw may be observed at a newer upstream revision.
	revision := int64(3)
	a.Revision, a.ExpectedVersion = &revision, second.Version
	third, err := f.apply(a)
	require.NoError(t, err)
	require.Equal(t, "applied", third.Outcome, "a repeated content hash is not an ordering rule")
	require.EqualValues(t, 3, third.Version)
	b.ExpectedVersion = third.Version
	stale, err := f.apply(b)
	require.NoError(t, err)
	require.Equal(t, "skipped_stale", stale.Outcome)
}

func TestOrderSourceIntegrationSameRawNewMappingCanReinterpret(t *testing.T) {
	f := newOrderFixture(t)
	s := f.snapshot(t, "reinterpretation")
	first, err := f.apply(s)
	require.NoError(t, err)
	s.Source.MappingChecksum, s.ExpectedVersion = "order-fixture-v2", first.Version
	s.Items.Rows[0].Title = "reinterpreted snapshot"
	result, err := f.apply(s)
	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
	require.Equal(t, "reinterpreted snapshot", f.items(t, result.OrderID)[0].Title)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var event order.StatusHistory
	require.NoError(t, q.Select("dimension,reason,evidence").Where("tenant_id=1 AND order_id=? AND reason='mapping_reinterpretation'", result.OrderID).Take(&event).Error)
	require.Equal(t, "aggregate", event.Dimension)
	require.Contains(t, string(event.Evidence), "order-fixture-v2")
}

func TestOrderSourceIntegrationRejectsUnverifiedRelationshipsAndPoolHandle(t *testing.T) {
	f := newOrderFixture(t)
	s := f.snapshot(t, "bound-source")
	before := f.aggregate(t)
	for _, kind := range []string{"tenant", "platform", "raw", "external"} {
		candidate := s
		switch kind {
		case "tenant":
			candidate.TenantID = 2
		case "platform":
			candidate.PlatformID++
		case "raw":
			candidate.Source.RawID += 10000
		case "external":
			candidate.ExternalOrderID = "other-instance"
		}
		_, err := f.apply(candidate)
		require.Equal(t, 403, errs.From(err).HTTPStatus)
		require.Equal(t, before, f.aggregate(t))
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	_, err := f.upserter.Apply(f.ctx, q, s)
	require.EqualError(t, err, "order transaction is required")
	ctx, cancelContext := context.WithCancel(f.ctx)
	cancelContext()
	err = f.database.Transaction(ctx, func(context.Context) error { return nil })
	require.Error(t, err)
}

func TestOrderSourceIntegrationDisplayNumberIsNotAnIdentity(t *testing.T) {
	f := newOrderFixture(t)
	first, err := f.apply(f.snapshot(t, "immutable-one"))
	require.NoError(t, err)
	second, err := f.apply(f.snapshot(t, "immutable-two"))
	require.NoError(t, err)
	require.Equal(t, "applied", first.Outcome)
	require.Equal(t, "applied", second.Outcome)
	require.NotEqual(t, first.OrderID, second.OrderID)
	unstable := f.snapshot(t, "untrusted")
	unstable.StableIdentity = false
	result, err := f.apply(unstable)
	require.NoError(t, err)
	require.Equal(t, "quarantined", result.Outcome)
	require.Equal(t, "unstable_order_identity", result.Reason)
}

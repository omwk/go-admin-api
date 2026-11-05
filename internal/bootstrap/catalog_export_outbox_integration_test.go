package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/stretchr/testify/require"
)

func TestExportIntegrationDeliveryLease(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	emitAuthorization(ctx, t, database)
	require.NoError(t, database.Transaction(ctx, func(ctx context.Context) error {
		return outbox.Emit(ctx, database, outbox.Event{TenantID: 1, AggregateType: "export_job", AggregateID: "1", EventType: "export.dispatch", Payload: []byte(`{"export_id":1}`)})
	}))
	publisher := &capturedPublisher{}
	dispatcher, err := outbox.NewDispatcher(database, publisher, nil)
	require.NoError(t, err)
	started := time.Now()
	require.NoError(t, dispatcher.RunOnce(ctx))
	require.Len(t, publisher.events, 2)
	for _, event := range publisher.events {
		lease := event.AvailableAt.Sub(started)
		if event.EventType == "export.dispatch" {
			require.Greater(t, lease, 5*time.Minute)
			require.LessOrEqual(t, lease, 6*time.Minute+time.Second)
		} else {
			require.Greater(t, lease, 59*time.Second)
			require.LessOrEqual(t, lease, 61*time.Second)
		}
	}
}

package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
)

// Event is the durable message envelope stored in outbox_event.
type Event struct {
	ID            int64
	TenantID      int64
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       json.RawMessage
	Attempts      int
	AvailableAt   time.Time
}

type Publisher interface {
	Publish(context.Context, Event) error
}
type Alert func(context.Context, Event, error)

// Emit persists an event using the transaction carried by ctx.
func Emit(ctx context.Context, database *db.DB, event Event) error {
	if database == nil || !db.InTransaction(ctx) {
		return errors.New("outbox emit requires a database transaction")
	}
	if event.TenantID <= 0 || event.AggregateType == "" || event.AggregateID == "" || event.EventType == "" {
		return errors.New("outbox event fields are required")
	}
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	}
	q, cancel := database.WithContext(ctx)
	defer cancel()
	return q.Table("outbox_event").Create(map[string]any{
		"tenant_id": event.TenantID, "aggregate_type": event.AggregateType, "aggregate_id": event.AggregateID,
		"event_type": event.EventType, "payload": event.Payload, "status": "pending", "attempts": 0,
		"available_at": time.Now().UTC(),
	}).Error
}

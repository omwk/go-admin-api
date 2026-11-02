package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/db"
)

// Consumer uses the durable event state for deduplication. The Redis lease only
// excludes concurrent deliveries; a crash never acknowledges an unfinished event.
type Consumer struct {
	dispatcher *Dispatcher
	shared     *cache.Cache
	handle     func(context.Context, Event) error
}

func NewConsumer(d *Dispatcher, shared *cache.Cache, handle func(context.Context, Event) error) (*Consumer, error) {
	if d == nil || shared == nil || handle == nil {
		return nil, errors.New("outbox consumer dependencies are required")
	}
	return &Consumer{dispatcher: d, shared: shared, handle: handle}, nil
}

func (c *Consumer) Handle(ctx context.Context, eventID int64, attempt int) (err error) {
	if eventID <= 0 || attempt <= 0 || db.InTransaction(ctx) {
		return errors.New("outbox delivery identifiers or context are invalid")
	}
	lease, err := c.shared.Lock(ctx, fmt.Sprintf("outbox:consume:%d", eventID), c.dispatcher.lease)
	if err != nil {
		return err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_, releaseErr := lease.Release(releaseCtx)
		err = errors.Join(err, releaseErr)
	}()
	q, cancel := c.dispatcher.database.WithContext(ctx)
	var events []Event
	err = q.Table("outbox_event").Select("id, tenant_id, aggregate_type, aggregate_id, event_type, payload, attempts, available_at").
		Where("id = ? AND attempts = ? AND status = ? AND available_at > ?", eventID, attempt, "processing", time.Now().UTC()).Find(&events).Error
	cancel()
	if err != nil || len(events) == 0 {
		return err // Sent, expired and superseded deliveries are safe no-ops.
	}
	event := events[0]
	ctx, cancel = context.WithDeadline(ctx, event.AvailableAt)
	defer cancel()
	if err := c.handle(ctx, event); err != nil {
		if ctx.Err() != nil {
			return ctx.Err() // Leave processing for lease recovery after interruption.
		}
		return errors.Join(err, c.dispatcher.markFailure(ctx, event, err))
	}
	return c.dispatcher.markSent(ctx, event)
}

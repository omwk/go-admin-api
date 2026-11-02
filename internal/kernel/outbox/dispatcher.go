package outbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultMaxAttempts = 10

type Dispatcher struct {
	database    *db.DB
	publisher   Publisher
	alert       Alert
	lease       time.Duration
	maxAttempts int
	batchSize   int
}

func NewDispatcher(database *db.DB, publisher Publisher, alert Alert) (*Dispatcher, error) {
	if database == nil || publisher == nil {
		return nil, errors.New("outbox dispatcher dependencies are required")
	}
	return &Dispatcher{database: database, publisher: publisher, alert: alert, lease: 60 * time.Second, maxAttempts: defaultMaxAttempts, batchSize: 100}, nil
}

func (d *Dispatcher) RunOnce(ctx context.Context) error {
	events, err := d.claim(ctx)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := d.publisher.Publish(ctx, event); err != nil {
			if markErr := d.markFailure(ctx, event, err); markErr != nil {
				return markErr
			}
			continue
		}
		if err := d.markSent(ctx, event.ID); err != nil {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) claim(ctx context.Context) ([]Event, error) {
	q, cancel := d.database.WithContext(ctx)
	defer cancel()
	var rows []Event
	err := q.Transaction(func(tx *gorm.DB) error {
		var ids []struct {
			ID int64 `gorm:"column:id"`
		}
		now := time.Now().UTC()
		if err := tx.Table("outbox_event").Select("id").Where("attempts < ? AND ((status IN ? AND available_at <= ?) OR (status = ? AND available_at <= ?))", d.maxAttempts, []string{"pending", "failed"}, now, "processing", now).Order("id asc").Limit(d.batchSize).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Find(&ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		idValues := make([]int64, 0, len(ids))
		for _, id := range ids {
			idValues = append(idValues, id.ID)
		}
		leaseUntil := now.Add(d.lease)
		if err := tx.Table("outbox_event").Where("id IN ?", idValues).Updates(map[string]any{"status": "processing", "available_at": leaseUntil, "attempts": gorm.Expr("attempts + 1")}).Error; err != nil {
			return err
		}
		return tx.Table("outbox_event").Select("id, tenant_id, aggregate_type, aggregate_id, event_type, payload, attempts, available_at").Where("id IN ?", idValues).Find(&rows).Error
	})
	return rows, err
}

func (d *Dispatcher) markSent(ctx context.Context, id int64) error {
	q, cancel := d.database.WithContext(ctx)
	defer cancel()
	return q.Table("outbox_event").Where("id = ? AND status = ?", id, "processing").Updates(map[string]any{"status": "sent", "sent_at": time.Now().UTC()}).Error
}

func (d *Dispatcher) markFailure(ctx context.Context, event Event, cause error) error {
	q, cancel := d.database.WithContext(ctx)
	defer cancel()
	if event.Attempts >= d.maxAttempts {
		if err := q.Table("outbox_event").Where("id = ? AND status = ?", event.ID, "processing").Updates(map[string]any{"status": "failed", "available_at": time.Now().UTC().Add(365 * 24 * time.Hour)}).Error; err != nil {
			return err
		}
		if d.alert != nil {
			d.alert(ctx, event, cause)
		}
		return nil
	}
	backoff := []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, 5 * time.Minute, 30 * time.Minute}
	index := event.Attempts - 1
	if index < 0 {
		index = 0
	}
	if index >= len(backoff) {
		index = len(backoff) - 1
	}
	return q.Table("outbox_event").Where("id = ? AND status = ?", event.ID, "processing").Updates(map[string]any{"status": "failed", "available_at": time.Now().UTC().Add(backoff[index])}).Error
}

// Consumer provides event-id idempotency for application handlers.
type Consumer struct {
	mu      sync.Mutex
	handled map[int64]struct{}
}

func (c *Consumer) Handle(ctx context.Context, event Event, fn func(context.Context, Event) error) error {
	if c == nil || fn == nil || event.ID <= 0 {
		return errors.New("outbox consumer input is invalid")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handled == nil {
		c.handled = map[int64]struct{}{}
	}
	if _, ok := c.handled[event.ID]; ok {
		return nil
	}
	if err := fn(ctx, event); err != nil {
		return fmt.Errorf("consume outbox event: %w", err)
	}
	c.handled[event.ID] = struct{}{}
	return nil
}

package outbox

import (
	"context"
	"errors"
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
	if db.InTransaction(ctx) {
		return errors.New("outbox dispatch cannot run inside a transaction")
	}
	if err := d.expireExhausted(ctx); err != nil {
		return err
	}
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
		deadline := gorm.Expr("CASE WHEN event_type = 'export.dispatch' THEN ?::timestamptz ELSE ?::timestamptz END", now.Add(6*time.Minute), leaseUntil)
		if err := tx.Table("outbox_event").Where("id IN ?", idValues).Updates(map[string]any{"status": "processing", "available_at": deadline, "attempts": gorm.Expr("attempts + 1")}).Error; err != nil {
			return err
		}
		return tx.Table("outbox_event").Select("id, tenant_id, aggregate_type, aggregate_id, event_type, payload, attempts, available_at").Where("id IN ?", idValues).Find(&rows).Error
	})
	return rows, err
}

func (d *Dispatcher) markSent(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	q, cancel := d.database.WithContext(ctx)
	defer cancel()
	result := q.Table("outbox_event").Where("id = ? AND status = ? AND attempts = ? AND available_at > ?", event.ID, "processing", event.Attempts, time.Now().UTC()).Updates(map[string]any{"status": "sent", "sent_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("outbox lease lost before completion")
	}
	return nil
}

func (d *Dispatcher) markFailure(ctx context.Context, event Event, cause error) error {
	q, cancel := d.database.WithContext(ctx)
	defer cancel()
	if event.Attempts >= d.maxAttempts {
		result := q.Table("outbox_event").Where("id = ? AND status = ? AND attempts = ?", event.ID, "processing", event.Attempts).Updates(map[string]any{"status": "failed", "available_at": gorm.Expr("'infinity'::timestamptz")})
		if err := result.Error; err != nil {
			return err
		}
		if result.RowsAffected > 0 && d.alert != nil {
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
	return q.Table("outbox_event").Where("id = ? AND status = ? AND attempts = ?", event.ID, "processing", event.Attempts).Updates(map[string]any{"status": "failed", "available_at": time.Now().UTC().Add(backoff[index])}).Error
}

// A crashed final attempt must also reach the terminal state and alert once.
func (d *Dispatcher) expireExhausted(ctx context.Context) error {
	q, cancel := d.database.WithContext(ctx)
	defer cancel()
	var events []Event
	err := q.Raw("UPDATE outbox_event SET status = 'failed', available_at = 'infinity'::timestamptz WHERE attempts >= ? AND status IN ('processing', 'failed', 'pending') AND available_at <= ? RETURNING id, attempts", d.maxAttempts, time.Now().UTC()).Scan(&events).Error
	if err != nil {
		return err
	}
	for _, event := range events {
		if d.alert != nil {
			d.alert(ctx, event, errors.New("outbox attempts exhausted"))
		}
	}
	return nil
}

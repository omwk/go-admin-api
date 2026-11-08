package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
)

func shareEventHandler(database *db.DB, fallback func(context.Context, outbox.Event) error) func(context.Context, outbox.Event) error {
	return func(ctx context.Context, event outbox.Event) error {
		if event.EventType != "share.inquiry.received" {
			return fallback(ctx, event)
		}
		var payload struct {
			InquiryID int64 `json:"inquiry_id"`
		}
		if event.TenantID <= 0 || json.Unmarshal(event.Payload, &payload) != nil || payload.InquiryID <= 0 {
			return errors.New("invalid inquiry receipt event")
		}
		q, cancel := database.WithContext(ctx)
		defer cancel()
		var count int64
		if err := q.Table("notification").Where("tenant_id=? AND channel='inbox' AND dedup_key=?", event.TenantID, fmt.Sprintf("inquiry:%d", payload.InquiryID)).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return errors.New("inquiry inbox receipt missing")
		}
		// The inbox is durable in the intake transaction; no external delivery occurs.
		return nil
	}
}

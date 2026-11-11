package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
)

func (n *accountNotifications) channelAvailability(ctx context.Context, d channel.Delivery, requestID string) error {
	providers, err := n.manager.store.Providers(ctx, d.TenantID, "email")
	if err != nil || len(providers) > 0 {
		return err
	}
	q, cancel := n.manager.store.database.WithContext(ctx)
	defer cancel()
	if err := q.Table("notify_delivery").Where("tenant_id=? AND id=?", d.TenantID, d.ID).Updates(map[string]any{
		"status": "skipped", "last_error": "channel_unavailable", "next_retry_at": n.manager.now().UTC().Add(5 * time.Minute),
	}).Error; err != nil {
		return err
	}
	if err := q.Table("notify_outbox").Where("tenant_id=? AND id=?", d.TenantID, d.ID).Update("status", "channel_unavailable").Error; err != nil {
		return err
	}
	if err := n.manager.store.administratorAlert(ctx, d); err != nil {
		return err
	}
	logger.From(ctx).WarnContext(ctx, "password reset channel unavailable", "module", "identity", "action", "password_reset_channel_unavailable",
		"request_id", requestID, "tenant_id", d.TenantID, "delivery_id", d.ID)
	return nil
}

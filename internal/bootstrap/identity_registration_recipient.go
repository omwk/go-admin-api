package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
)

func (m *notificationManager) accountMessageRecipient(ctx context.Context, tenant int64, in channel.Request) (string, error) {
	if in.TemplateCode != "identity.verify" {
		return m.store.Recipient(ctx, tenant, in.UserID, in.Channel)
	}
	if in.Channel != "email" {
		return "", errors.New("verification recipient unavailable")
	}
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	var row struct{ Email *string }
	err := q.Table("sys_user").Select("email").Where("tenant_id=? AND id=? AND status='disabled' AND deleted_at IS NULL", tenant, in.UserID).
		Where(`EXISTS(SELECT 1 FROM auth_token WHERE auth_token.tenant_id=sys_user.tenant_id AND auth_token.user_id=sys_user.id
 AND purpose='verify' AND used_at IS NULL AND revoked_at IS NULL AND expires_at>?)`, m.now().UTC()).Take(&row).Error
	if err != nil || row.Email == nil || *row.Email == "" {
		return "", errors.New("verification recipient unavailable")
	}
	return *row.Email, nil
}

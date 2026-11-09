package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"gorm.io/gorm"
)

func (n *credentialNotifications) recovered(ctx context.Context, event outbox.Event) error {
	var payload struct {
		CredentialID int64 `json:"credential_id"`
		VersionID    int64 `json:"version_id"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil || payload.CredentialID <= 0 || payload.VersionID <= 0 || event.TenantID <= 0 || event.AggregateType != "credential" || event.AggregateID != strconv.FormatInt(payload.CredentialID, 10) {
		return errors.New("invalid credential recovery identifiers")
	}
	q, cancel := n.runtime.database.WithContext(ctx)
	defer cancel()
	var alert credentialAlert
	err := q.Table("shop_credential c").Select("c.tenant_id,c.id AS credential_id,c.shop_id,s.owner_user_id,p.code AS platform").
		Joins("JOIN shop s ON s.tenant_id=c.tenant_id AND s.id=c.shop_id AND s.deleted_at IS NULL").Joins("JOIN platform p ON p.id=s.platform_id").
		Where(`c.tenant_id=? AND c.id=? AND c.current_version_id=? AND c.status IN ('active','expiring')
 AND (c.expires_at IS NULL OR c.expires_at>?) AND EXISTS (SELECT 1 FROM notification n
 WHERE n.tenant_id=c.tenant_id AND n.credential_id=c.id AND n.channel='inbox' AND n.template<>'credential.recovered'
 AND n.id>COALESCE((SELECT max(recovery.id) FROM notification recovery WHERE recovery.tenant_id=c.tenant_id
 AND recovery.credential_id=c.id AND recovery.channel='inbox' AND recovery.template='credential.recovered'),0))`, event.TenantID, payload.CredentialID, payload.VersionID, n.now().UTC()).Take(&alert).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return credentialDBError(err)
	}
	alert.Template, alert.Severity = "credential.recovered", "info"
	return n.notify(ctx, alert)
}

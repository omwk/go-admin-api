package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (n *credentialNotifications) deliverOne(ctx context.Context) (bool, error) {
	var row credentialNotification
	found := false
	err := n.runtime.database.Transaction(ctx, func(tx context.Context) error {
		q, cancel := n.runtime.database.WithContext(tx)
		defer cancel()
		err := q.Table("notification").Select("id,tenant_id,channel,target,template,payload,shop_id,credential_id,attempts").
			Where("channel IN ('email','webhook') AND credential_id IS NOT NULL AND status='pending' AND available_at<=?", n.now().UTC()).
			Order("id").Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return credentialDBError(err)
		}
		found = true
		if row.Attempts >= 3 {
			return q.Table("notification").Where("id=?", row.ID).Updates(map[string]any{"status": "failed", "last_error": "delivery attempts exhausted"}).Error
		}
		row.Attempts++
		return q.Table("notification").Where("id=?", row.ID).Updates(map[string]any{"attempts": row.Attempts, "available_at": n.now().UTC().Add(30 * time.Second)}).Error
	})
	if err != nil || !found || row.Attempts > 3 {
		return found, err
	}
	if row.Attempts == 3 {
		q, cancel := n.runtime.database.WithContext(ctx)
		defer cancel()
		var status string
		if err := q.Table("notification").Select("status").Where("id=?", row.ID).Take(&status).Error; err != nil {
			return true, err
		}
		if status == "failed" {
			return true, nil
		}
	}
	delivered, err := n.sendRow(ctx, row)
	status, last := "pending", "delivery failed"
	next := n.now().UTC().Add(time.Duration(row.Attempts) * time.Minute)
	values := map[string]any{"available_at": next}
	if delivered {
		status, last = "sent", ""
		values["sent_at"] = n.now().UTC()
	} else if row.Attempts >= 3 || errors.Is(err, errNotificationRecipient) || errors.Is(err, errNotificationDisabled) {
		status = "failed"
	}
	if errors.Is(err, errNotificationDisabled) {
		last = "preference_disabled"
	}
	values["status"], values["last_error"] = status, last
	q, cancel := n.runtime.database.WithContext(ctx)
	defer cancel()
	return true, credentialDBError(q.Table("notification").Where("id=? AND attempts=? AND status='pending'", row.ID, row.Attempts).Updates(values).Error)
}

var errNotificationRecipient = errors.New("notification recipient unavailable")
var errNotificationDisabled = errors.New("notification preference disabled")

func (n *credentialNotifications) sendRow(ctx context.Context, row credentialNotification) (bool, error) {
	owner, err := strconv.ParseInt(strings.TrimPrefix(row.Target, "user:"), 10, 64)
	if err != nil || owner <= 0 || row.ShopID == nil {
		return false, errNotificationRecipient
	}
	if row.Channel != "webhook" {
		allowed, err := notificationPreferenceAllowed(ctx, n.runtime.database, row.TenantID, owner, row.Template, row.Channel)
		if err != nil {
			return false, err
		}
		if !allowed {
			return false, errNotificationDisabled
		}
	}
	q, cancel := n.runtime.database.WithContext(ctx)
	defer cancel()
	var shopOwner int64
	if err := q.Table("shop").Select("owner_user_id").Where("tenant_id=? AND id=? AND deleted_at IS NULL", row.TenantID, *row.ShopID).Take(&shopOwner).Error; err != nil {
		return false, errNotificationRecipient
	}
	if shopOwner != owner {
		return false, errNotificationRecipient
	}
	if err := n.runtime.authorizeActor(ctx, row.TenantID, owner, *row.ShopID); err != nil {
		return false, errNotificationRecipient
	}
	var user struct{ Email *string }
	if err := q.Table("sys_user").Select("email").Where("tenant_id=? AND id=? AND status='active' AND deleted_at IS NULL", row.TenantID, owner).Take(&user).Error; err != nil {
		return false, errNotificationRecipient
	}
	settings, err := n.settings(ctx, row.TenantID)
	if err != nil {
		return false, err
	}
	var message notifier.Message
	if json.Unmarshal(row.Payload, &message) != nil {
		return false, errors.New("invalid notification metadata")
	}
	message.EventID = row.ID
	if row.Channel == "webhook" {
		return n.sendPersonalRow(ctx, row, owner, message, settings)
	}
	recipient := ""
	if user.Email != nil {
		recipient = *user.Email
	}
	if err := n.send(ctx, row.Channel, settings, recipient, message); err != nil {
		return false, err
	}
	return true, nil
}

func (n *credentialNotifications) sendPersonalRow(ctx context.Context, row credentialNotification, owner int64, message notifier.Message, settings notifier.Settings) (bool, error) {
	m := notificationManagerForRuntime(n.runtime)
	r, err := m.routePersonalNotification(ctx, row.TenantID, channel.Request{Channel: row.Channel, UserID: owner, TemplateCode: row.Template})
	if err != nil {
		return false, err
	}
	allowed, err := notificationPreferenceAllowed(ctx, n.runtime.database, row.TenantID, owner, row.Template, r.Channel)
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, errNotificationDisabled
	}
	recipient, err := m.store.Recipient(ctx, row.TenantID, owner, r.Channel)
	if err != nil {
		return false, errNotificationRecipient
	}
	if r.Channel == "email" {
		err = n.send(ctx, "email", settings, recipient, message)
	} else {
		body, encodeErr := json.Marshal(message)
		if encodeErr != nil {
			return false, errors.New("invalid notification metadata")
		}
		err = n.personal.Send(ctx, r.Channel, recipient, channel.Message{Subject: message.Template, Body: string(body)})
	}
	return err == nil, err
}

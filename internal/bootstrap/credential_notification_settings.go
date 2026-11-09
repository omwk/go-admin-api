package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type credentialNotifications struct {
	runtime *credentialRefreshRuntime
	now     func() time.Time
	send    func(context.Context, string, notifier.Settings, string, notifier.Message) error
}

func newCredentialNotifications(r *credentialRefreshRuntime) *credentialNotifications {
	return &credentialNotifications{runtime: r, now: r.now, send: sendCredentialNotification}
}

func sendCredentialNotification(ctx context.Context, channel string, s notifier.Settings, email string, m notifier.Message) error {
	switch channel {
	case "email":
		return (notifier.Email{Settings: s, Recipient: email}).Send(ctx, m)
	case "webhook":
		return (notifier.Webhook{Settings: s}).Send(ctx, m)
	default:
		return errors.New("unsupported notification channel")
	}
}

func notificationSettingsAAD(tenant int64) string {
	return fmt.Sprintf("credential-notification-settings:v1:tenant:%d", tenant)
}

func (n *credentialNotifications) channels(ctx context.Context, tenant int64) (notifier.SettingsView, error) {
	var result notifier.SettingsView
	q, cancel := n.runtime.database.WithContext(ctx)
	defer cancel()
	err := q.Table("credential_notification_setting").Select("email_enabled,webhook_enabled").Where("tenant_id=?", tenant).Take(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	return result, credentialDBError(err)
}

func (n *credentialNotifications) settings(ctx context.Context, tenant int64) (notifier.Settings, error) {
	var result notifier.Settings
	var stored struct {
		CipherText, Nonce []byte
		KeyVersion        int
	}
	q, cancel := n.runtime.database.WithContext(ctx)
	defer cancel()
	err := q.Table("credential_notification_setting").Select("cipher_text,nonce,key_version").Where("tenant_id=?", tenant).Take(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return result, credentialDBError(err)
	}
	if n.runtime.sealer == nil {
		return result, errs.New(30501, 500, "通知配置加密不可用")
	}
	plain, err := n.runtime.sealer.Open(stored.CipherText, stored.Nonce, notificationSettingsAAD(tenant), stored.KeyVersion)
	if err != nil {
		return result, errs.New(30501, 500, "通知配置解密失败")
	}
	defer clear(plain)
	if json.Unmarshal(plain, &result) != nil {
		return result, errs.New(10002, 500, "通知配置不可用")
	}
	return result, nil
}

func (n *credentialNotifications) authorizeSettings(ctx context.Context, tenant, user int64) error {
	profiles, err := identity.NewProfileService(newProfileStore(n.runtime.database))
	if err != nil {
		return err
	}
	p, err := profiles.Me(ctx, tenant, user)
	if err != nil {
		return err
	}
	if !p.User.IsSuper && !slices.Contains(p.Permissions, permission.SystemSettingWrite) {
		return scope.Denied()
	}
	return nil
}

func (n *credentialNotifications) Settings(ctx context.Context, tenant, user int64) (notifier.SettingsView, error) {
	if err := n.authorizeSettings(ctx, tenant, user); err != nil {
		return notifier.SettingsView{}, err
	}
	s, err := n.settings(ctx, tenant)
	return s.View(), err
}

func (n *credentialNotifications) SaveSettings(ctx context.Context, tenant, user int64, s notifier.Settings) error {
	if err := n.authorizeSettings(ctx, tenant, user); err != nil {
		return err
	}
	if err := s.Validate(); err != nil {
		return errs.New(10001, 400, "通知配置无效")
	}
	if n.runtime.sealer == nil {
		return errs.New(30501, 500, "通知配置加密不可用")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	defer clear(raw)
	cipher, nonce, key, err := n.runtime.sealer.Seal(raw, notificationSettingsAAD(tenant))
	if err != nil {
		return err
	}
	return n.runtime.database.Transaction(ctx, func(tx context.Context) error {
		if err := n.authorizeSettings(tx, tenant, user); err != nil {
			return err
		}
		q, cancel := n.runtime.database.WithContext(tx)
		defer cancel()
		row := map[string]any{"tenant_id": tenant, "email_enabled": s.EmailEnabled, "webhook_enabled": s.WebhookEnabled, "cipher_text": cipher, "nonce": nonce, "key_version": key, "updated_at": n.now().UTC()}
		err := q.Table("credential_notification_setting").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}}, DoUpdates: clause.AssignmentColumns([]string{"email_enabled", "webhook_enabled", "cipher_text", "nonce", "key_version", "updated_at"})}).Create(row).Error
		if err != nil {
			return credentialDBError(err)
		}
		return audit.Record(tx, n.runtime.database, audit.Event{TenantID: tenant, ActorType: "user", ActorID: &user, Action: "notification.settings.update", ResourceType: "notification_settings", ResourceID: fmt.Sprint(tenant), Result: "success"})
	})
}

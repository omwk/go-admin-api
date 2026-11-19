package bootstrap

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type notificationPreference struct {
	TenantID, UserID     int64
	Template, Channel    string
	Enabled              bool
	CreatedAt, UpdatedAt time.Time
}

func notificationPreferenceAllowed(ctx context.Context, database *db.DB, tenant, user int64, template, kind string) (bool, error) {
	if channel.Transactional(template) {
		return true, nil
	}
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var enabled bool
	err := q.Table("notification_preference").Select("enabled").Where("tenant_id=? AND user_id=? AND template=? AND channel=?", tenant, user, template, kind).Take(&enabled).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil
	}
	return enabled, notificationDBError(err)
}

type preferenceTemplate struct{ Code, Name string }

func (n *credentialNotifications) preferenceTemplates(ctx context.Context, tenant int64) ([]preferenceTemplate, error) {
	q, cancel := n.runtime.database.WithContext(ctx)
	defer cancel()
	rows := []preferenceTemplate{}
	err := q.Table("notify_template").Select("DISTINCT ON (code) code,COALESCE(NULLIF(name,''),NULLIF(subject,''),code) AS name").
		Where("tenant_id=? OR tenant_id IS NULL", tenant).Order("code,tenant_id NULLS LAST,locale").Find(&rows).Error
	return rows, notificationDBError(err)
}

func (n *credentialNotifications) preferenceDefaults(ctx context.Context, tenant int64) (map[string]bool, error) {
	settings, err := n.channels(ctx, tenant)
	if err != nil {
		return nil, err
	}
	q, cancel := n.runtime.database.WithContext(ctx)
	defer cancel()
	var managed []string
	err = q.Table("channel_provider").Where("(tenant_id=? OR tenant_id IS NULL) AND enabled", tenant).Distinct("channel").Pluck("channel", &managed).Error
	defaults := map[string]bool{"email": settings.EmailEnabled, "webhook": settings.WebhookEnabled}
	for _, kind := range managed {
		defaults[kind] = true
	}
	return defaults, notificationDBError(err)
}

func (n *credentialNotifications) Preferences(ctx context.Context, tenant, user int64) ([]notifier.PreferenceView, error) {
	templates, err := n.preferenceTemplates(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defaults, err := n.preferenceDefaults(ctx, tenant)
	if err != nil {
		return nil, err
	}
	q, cancel := n.runtime.database.WithContext(ctx)
	defer cancel()
	var stored []notificationPreference
	err = q.Table("notification_preference").Select("template,channel,enabled").Where("tenant_id=? AND user_id=?", tenant, user).Find(&stored).Error
	if err != nil {
		return nil, notificationDBError(err)
	}
	overrides := make(map[[2]string]bool, len(stored))
	for _, row := range stored {
		overrides[[2]string{row.Template, row.Channel}] = row.Enabled
	}
	rows := make([]notifier.PreferenceView, 0, len(templates)*len(notifier.ExternalChannels()))
	for _, template := range templates {
		for _, kind := range notifier.ExternalChannels() {
			enabled, set := overrides[[2]string{template.Code, kind}]
			if !set {
				enabled = defaults[kind]
			}
			if channel.Transactional(template.Code) {
				enabled = true
			}
			rows = append(rows, notifier.PreferenceView{Template: template.Code, Name: template.Name, Transactional: channel.Transactional(template.Code), Channel: kind, Enabled: enabled, Inherited: !set})
		}
	}
	return rows, nil
}

func validatePreferences(input []notifier.Preference, templates []preferenceTemplate) error {
	if len(input) == 0 || len(input) > 500 {
		return errs.New(10001, 400, "通知偏好数量无效")
	}
	seen := map[[2]string]bool{}
	for _, item := range input {
		key := [2]string{item.Template, item.Channel}
		if item.Enabled == nil || !slices.ContainsFunc(templates, func(t preferenceTemplate) bool { return t.Code == item.Template }) || !slices.Contains(notifier.ExternalChannels(), item.Channel) || seen[key] {
			return errs.New(10001, 400, "通知偏好模板或渠道无效")
		}
		if channel.Transactional(item.Template) && !*item.Enabled {
			return errs.New(10004, 409, "安全邮件不可关闭")
		}
		seen[key] = true
	}
	return nil
}

func (n *credentialNotifications) SavePreferences(ctx context.Context, tenant, user int64, input []notifier.Preference) ([]notifier.PreferenceView, error) {
	var result []notifier.PreferenceView
	err := n.runtime.database.Transaction(ctx, func(tx context.Context) error {
		templates, err := n.preferenceTemplates(tx, tenant)
		if err != nil {
			return err
		}
		if err := validatePreferences(input, templates); err != nil {
			return err
		}
		rows := make([]notificationPreference, 0, len(input))
		for _, item := range input {
			rows = append(rows, notificationPreference{TenantID: tenant, UserID: user, Template: item.Template, Channel: item.Channel,
				Enabled: *item.Enabled, CreatedAt: n.now().UTC(), UpdatedAt: n.now().UTC()})
		}
		q, cancel := n.runtime.database.WithContext(tx)
		defer cancel()
		err = q.Table("notification_preference").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "template"}, {Name: "channel"}},
			DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_at"})}).CreateInBatches(&rows, 500).Error
		if err != nil {
			return notificationDBError(err)
		}
		if err := RecordAudit(tx, n.runtime.database, audit.Event{TenantID: tenant, ActorType: "user", ActorID: &user, Action: "notification.preferences.update",
			ResourceType: "notification_preference", ResourceID: fmtNotificationID(user), Result: "success", After: map[string]any{"count": len(rows)}}); err != nil {
			return err
		}
		result, err = n.Preferences(tx, tenant, user)
		return err
	})
	return result, notificationDBError(err)
}

package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type credentialAlert struct {
	TenantID, CredentialID, ShopID, OwnerUserID int64
	Platform, Template, Severity                string
}

type credentialNotification struct {
	ID           int64           `json:"id"`
	TenantID     int64           `json:"-"`
	Target       string          `json:"-"`
	Channel      string          `json:"channel"`
	Template     string          `json:"template"`
	Payload      json.RawMessage `json:"payload"`
	Status       string          `json:"status"`
	Attempts     int             `json:"attempts"`
	LastError    string          `json:"last_error"`
	ReadAt       *time.Time      `json:"read_at"`
	CreatedAt    time.Time       `json:"created_at"`
	AvailableAt  time.Time       `json:"-"`
	SentAt       *time.Time      `json:"-"`
	ShopID       *int64          `json:"-"`
	CredentialID *int64          `json:"credential_id"`
}

func (n *credentialNotifications) notify(ctx context.Context, a credentialAlert) error {
	if err := n.runtime.authorizeActor(ctx, a.TenantID, a.OwnerUserID, a.ShopID); err != nil {
		var app *errs.AppError
		if errors.As(err, &app) && (app.HTTPStatus == 403 || app.HTTPStatus == 404 || app.HTTPStatus == 401) {
			return nil
		}
		return err
	}
	settings, err := n.channels(ctx, a.TenantID)
	if err != nil {
		return err
	}
	managed, err := n.managedChannels(ctx, a.TenantID)
	if err != nil {
		return err
	}
	channels := []string{"inbox"}
	if a.Severity != "info" {
		if settings.EmailEnabled && !managed["email"] {
			channels = append(channels, "email")
		}
		if settings.WebhookEnabled && !managed["webhook"] {
			channels = append(channels, "webhook")
		}
	}
	return n.runtime.database.Transaction(ctx, func(tx context.Context) error {
		q, cancel := n.runtime.database.WithContext(tx)
		defer cancel()
		var owner int64
		if err := q.Table("shop").Select("owner_user_id").Where("tenant_id=? AND id=? AND deleted_at IS NULL", a.TenantID, a.ShopID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&owner).Error; err != nil {
			return credentialDBError(err)
		}
		if owner != a.OwnerUserID {
			return nil
		}
		for _, channel := range channels {
			if err := n.insertNotification(q, a, channel); err != nil {
				return err
			}
		}
		return nil
	})
}

func (n *credentialNotifications) insertNotification(q *gorm.DB, a credentialAlert, channel string) error {
	if channel != "inbox" {
		allowed, err := notificationPreferenceAllowed(q.Statement.Context, n.runtime.database, a.TenantID, a.OwnerUserID, a.Template, channel)
		if err != nil || !allowed {
			return err
		}
	}
	now := n.now().UTC()
	target := "user:" + strconv.FormatInt(a.OwnerUserID, 10)
	subject := strconv.FormatInt(a.CredentialID, 10)
	if channel != "inbox" && (a.Template == "credential.failed" || a.Template == "credential.stuck" || a.Template == "credential.protocol_pending") {
		subject = "platform:" + a.Platform
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%s:%s", channel, target, a.Template, a.Severity, subject)))
	key := hex.EncodeToString(sum[:])
	result := q.Exec(`INSERT INTO credential_notification_dedup(tenant_id,event_key,notified_at) VALUES(?,?,?)
 ON CONFLICT(tenant_id,event_key) DO UPDATE SET notified_at=excluded.notified_at
 WHERE credential_notification_dedup.notified_at<=?`, a.TenantID, key, now, now.Add(-24*time.Hour))
	if result.Error != nil {
		return credentialDBError(result.Error)
	}
	if result.RowsAffected == 0 {
		return nil
	}
	row := credentialNotification{TenantID: a.TenantID, Channel: channel, Target: target, Template: a.Template, Payload: json.RawMessage("{}"),
		ShopID: &a.ShopID, CredentialID: &a.CredentialID, CreatedAt: now, AvailableAt: now, Status: "pending"}
	if channel == "inbox" {
		row.Status = "sent"
		row.SentAt = &now
	}
	if err := q.Table("notification").Create(&row).Error; err != nil {
		return credentialDBError(err)
	}
	message := notifier.Message{EventID: row.ID, Template: a.Template, Platform: a.Platform, Severity: a.Severity, Path: "/admin/notifications"}
	if err := message.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if err := credentialDBError(q.Table("notification").Where("id=?", row.ID).Update("payload", json.RawMessage(payload)).Error); err != nil {
		return err
	}
	if channel == "inbox" {
		return n.queueManaged(q.Statement.Context, a)
	}
	return nil
}

func (n *credentialNotifications) notificationQuery(ctx context.Context, tenant, user int64) (*gorm.DB, context.CancelFunc, error) {
	q, cancel := n.runtime.database.WithContext(ctx)
	profiles, err := identity.NewProfileService(newProfileStore(n.runtime.database))
	if err != nil {
		return nil, cancel, err
	}
	p, err := profiles.Me(ctx, tenant, user)
	if err != nil {
		return nil, cancel, err
	}
	q = q.Table("notification n").Where("n.tenant_id=? AND n.target=?", tenant, "user:"+strconv.FormatInt(user, 10))
	if p.User.IsSuper {
		return q, cancel, nil
	}
	if !slices.Contains(p.Permissions, permission.ShopCredentialRotate) {
		return q.Where("n.credential_id IS NULL"), cancel, nil
	}
	human, err := newAdminStore(n.runtime.database).Authorize(ctx, tenant, user)
	if err != nil {
		return nil, cancel, err
	}
	root, rootCancel := n.runtime.database.WithContext(human)
	defer rootCancel()
	shops, err := shopScope(human, model.Shop{}, root.Table("shop").Select("shop.id").Where("shop.owner_user_id=?", user))
	if err != nil {
		return nil, cancel, err
	}
	return q.Where("n.credential_id IS NULL OR n.shop_id IN (?)", shops), cancel, nil
}

func (n *credentialNotifications) inboxQuery(ctx context.Context, tenant, user int64) (*gorm.DB, context.CancelFunc, error) {
	q, cancel, err := n.notificationQuery(ctx, tenant, user)
	if err == nil {
		q = q.Where("n.channel='inbox'")
	}
	return q, cancel, err
}

func (n *credentialNotifications) Inbox(ctx context.Context, tenant, user int64, page, size int, filter notifier.InboxFilter) (any, error) {
	q, cancel, err := n.notificationQuery(ctx, tenant, user)
	defer cancel()
	if err != nil {
		return nil, err
	}
	if filter.Channel == "" {
		filter.Channel = "inbox"
	}
	q = q.Where("n.channel=?", filter.Channel)
	if filter.Template != "" {
		q = q.Where("n.template=?", filter.Template)
	}
	if filter.Unread != nil {
		if *filter.Unread {
			q = q.Where("n.read_at IS NULL")
		} else {
			q = q.Where("n.read_at IS NOT NULL")
		}
	}
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, credentialDBError(err)
	}
	rows := []credentialNotification{}
	err = q.Select("n.id,n.channel,n.template,n.payload,n.status,n.attempts,n.last_error,n.read_at,n.created_at,n.credential_id").Order("n.created_at DESC,n.id DESC").Limit(size).Offset((page - 1) * size).Find(&rows).Error
	return struct {
		List     []credentialNotification `json:"list"`
		Total    int64                    `json:"total"`
		Page     int                      `json:"page"`
		PageSize int                      `json:"page_size"`
	}{rows, total, page, size}, credentialDBError(err)
}

func (n *credentialNotifications) Read(ctx context.Context, tenant, user, id int64) error {
	q, cancel, err := n.inboxQuery(ctx, tenant, user)
	defer cancel()
	if err != nil {
		return err
	}
	result := q.Where("n.id=?", id).Update("read_at", gorm.Expr("COALESCE(read_at, ?)", n.now().UTC()))
	if result.Error != nil {
		return credentialDBError(result.Error)
	}
	if result.RowsAffected == 0 {
		return errs.New(10003, 404, "通知不存在")
	}
	return nil
}

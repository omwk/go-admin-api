package bootstrap

import (
	"context"
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

func registerNotificationManagement(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, r *credentialRefreshRuntime) error {
	guard, err := identity.RequirePerm(profiles, permission.SystemSettingWrite)
	if err != nil {
		return err
	}
	actor, err := identity.RequireActor(profiles)
	if err != nil {
		return err
	}
	m := newNotificationManager(&notificationStore{database: r.database}, r.sealer, r.now)
	channel.RegisterManagement(router, m, channel.Access{Authenticate: gin.HandlersChain{identity.JWTAuth(auth), actor}, Permission: guard,
		Actor: func(c *gin.Context) channel.Actor {
			a := identity.AdminActor(c)
			return channel.Actor{TenantID: a.User.TenantID, UserID: a.User.ID, Super: a.User.IsSuper}
		}})
	return nil
}

func (m *notificationManager) drain(ctx context.Context, log *slog.Logger) {
	for i := 0; i < 100; i++ {
		d, err := m.delivery.Deliver(ctx, 0, 0)
		if err != nil {
			if ctx.Err() == nil {
				log.ErrorContext(ctx, "notification outbox dispatch failed", "module", "notification", "action", "dispatch")
			}
			return
		}
		if d.ID == 0 {
			return
		}
	}
}

func (n *credentialNotifications) queueManaged(ctx context.Context, a credentialAlert) error {
	if a.Severity == "info" {
		return nil
	}
	legacy, err := n.channels(ctx, a.TenantID)
	if err != nil {
		return err
	}
	configured, err := n.managedChannels(ctx, a.TenantID)
	if err != nil {
		return err
	}
	m := newNotificationManager(&notificationStore{database: n.runtime.database}, n.runtime.sealer, n.now)
	channels := []string{}
	for _, kind := range []string{"email", "sms", "wecom", "dingtalk", "webhook"} {
		if configured[kind] {
			channels = append(channels, kind)
		}
	}
	if len(channels) == 0 && !legacy.EmailEnabled && !legacy.WebhookEnabled {
		channels = []string{"email"}
	}
	for _, kind := range channels {
		_, err := m.delivery.Enqueue(ctx, a.TenantID, channel.Request{Channel: kind, TemplateCode: a.Template, UserID: a.OwnerUserID,
			Variables: map[string]string{"Platform": a.Platform, "Severity": a.Severity, "Path": "/admin/notifications"}})
		if err != nil {
			return err
		}
	}
	return nil
}

func (n *credentialNotifications) managedChannels(ctx context.Context, tenant int64) (map[string]bool, error) {
	q, cancel := n.runtime.database.WithContext(ctx)
	defer cancel()
	var channels []string
	err := q.Table("channel_provider").Where("tenant_id=? OR tenant_id IS NULL", tenant).Distinct("channel").Pluck("channel", &channels).Error
	configured := make(map[string]bool, len(channels))
	for _, kind := range channels {
		configured[kind] = true
	}
	return configured, notificationDBError(err)
}

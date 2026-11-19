package bootstrap

import (
	"context"
	"log/slog"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

func registerNotificationManagement(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, r *credentialRefreshRuntime) error {
	guard, err := identity.RequirePerm(profiles, permission.SystemNotifyManage)
	if err != nil {
		return err
	}
	list, err := identity.RequirePerm(profiles, permission.SystemNotifyList)
	if err != nil {
		return err
	}
	actor, err := identity.RequireActor(profiles)
	if err != nil {
		return err
	}
	broadcast, err := identity.RequirePerm(profiles, permission.SystemNotifyBroadcast)
	if err != nil {
		return err
	}
	m := notificationManagerForRuntime(r)
	if r.platformTenantID > 0 {
		m.platformTenantID = r.platformTenantID
	}
	access := channel.Access{Authenticate: gin.HandlersChain{identity.JWTAuth(auth), actor}, Permission: guard, ListPermission: list,
		Actor: func(c *gin.Context) channel.Actor {
			a := identity.AdminActor(c)
			return channel.Actor{TenantID: a.User.TenantID, UserID: a.User.ID, Super: a.User.IsSuper}
		}}
	channel.RegisterManagement(router, m, access)
	channel.RegisterPersonal(router, m, access)
	notifier.RegisterPersonalPreferences(router, newCredentialNotifications(r), notifier.HTTPAccess{Authenticate: access.Authenticate,
		Actor: func(c *gin.Context) (int64, int64) { a := access.Actor(c); return a.TenantID, a.UserID }})
	channel.RegisterBroadcasts(router, m, access, broadcast)
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
	m := notificationManagerForRuntime(n.runtime)
	channels := []string{}
	for _, kind := range []string{"email", "wecom", "dingtalk", "webhook"} {
		if configured[kind] {
			channels = append(channels, kind)
		}
	}
	personal, err := m.store.personalChannels(ctx, a.TenantID, a.OwnerUserID)
	if err != nil {
		return err
	}
	for _, row := range personal {
		if row.Enabled && row.VerifiedAt != nil && !slices.Contains(channels, row.Channel) {
			channels = append(channels, row.Channel)
		}
	}
	if len(channels) == 0 && !legacy.EmailEnabled && !legacy.WebhookEnabled {
		channels = []string{"email"}
	}
	for _, kind := range channels {
		_, err := m.delivery.Enqueue(ctx, a.TenantID, channel.Request{Channel: kind, TemplateCode: a.Template, UserID: a.OwnerUserID,
			Variables: credentialNotificationVariables(a)})
		if err != nil {
			return err
		}
	}
	return nil
}

func notificationManagerForRuntime(r *credentialRefreshRuntime) *notificationManager {
	m := newNotificationManager(&notificationStore{database: r.database}, r.sealer, r.now)
	m.delivery.Personal = newPersonalNotificationTransport(r)
	return m
}

func newPersonalNotificationTransport(r *credentialRefreshRuntime) *channel.PersonalTransport {
	return &channel.PersonalTransport{AllowedHosts: map[string][]string{
		"webhook": r.notification.WebhookAllowedHosts, "bark": r.notification.BarkAllowedHosts, "gotify": r.notification.GotifyAllowedHosts,
	}}
}

func credentialNotificationVariables(a credentialAlert) map[string]string {
	return map[string]string{"Platform": a.Platform, "Severity": a.Severity, "Path": "/admin/notifications"}
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

package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
)

func (m *notificationManager) routePersonalNotification(ctx context.Context, tenant int64, in channel.Request) (channel.Request, error) {
	if !channel.PersonalChannel(in.Channel) {
		return in, nil
	}
	allowed, err := notificationPreferenceAllowed(ctx, m.store.database, tenant, in.UserID, in.TemplateCode, in.Channel)
	if err != nil {
		return in, err
	}
	row, err := m.store.personalChannel(ctx, tenant, in.UserID, in.Channel)
	if err != nil {
		var app *errs.AppError
		if !errors.As(err, &app) || app.HTTPStatus != 404 {
			return in, err
		}
	}
	if !allowed || err != nil || !row.Enabled || row.VerifiedAt == nil {
		in.Channel, in.ProviderID = "email", 0
	}
	return in, nil
}

type personalAvailability struct {
	Available bool
	Reason    string
}

func (n *credentialNotifications) personalAvailability(ctx context.Context, tenant, user int64) (map[string]personalAvailability, error) {
	store := &notificationStore{database: n.runtime.database, sealer: n.runtime.sealer}
	rows, err := store.personalChannels(ctx, tenant, user)
	if err != nil {
		return nil, err
	}
	available := map[string]personalAvailability{
		"email": {Reason: "missing_email"}, "webhook": {Reason: "not_configured"},
		"bark": {Reason: "not_configured"}, "gotify": {Reason: "not_configured"},
	}
	q, cancel := n.runtime.database.WithContext(ctx)
	defer cancel()
	var mailbox struct{ Email *string }
	if err := q.Table("sys_user").Select("email").Where("tenant_id=? AND id=? AND deleted_at IS NULL", tenant, user).Take(&mailbox).Error; err != nil {
		return nil, notificationDBError(err)
	}
	if mailbox.Email != nil && *mailbox.Email != "" {
		available["email"] = personalAvailability{Available: true}
	}
	for _, row := range rows {
		state := personalAvailability{Available: true}
		switch {
		case !row.Enabled:
			state = personalAvailability{Reason: "disabled"}
		case row.VerifiedAt == nil:
			state = personalAvailability{Reason: "not_verified"}
		}
		available[row.Channel] = state
	}
	return available, nil
}

package bootstrap

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
)

var _ identity.EmailChangeNotifications = (*accountNotifications)(nil)

func (n *accountNotifications) QueueEmailChange(ctx context.Context, row model.AuthToken, link string, meta identity.RequestMeta) error {
	if n.manager.sealer == nil || row.UserID == nil || row.NewEmail == nil {
		return errors.New("email change notifications unavailable")
	}
	data, err := n.messageData(ctx, row.TenantID, *row.UserID, row.ID, "email_change")
	if err != nil {
		return err
	}
	data.NewEmail, data.IP, data.UserAgent = *row.NewEmail, meta.IP, meta.UserAgent
	support, err := setting.SupportEmail(ctx, &systemSettingStore{database: n.manager.store.database}, row.TenantID)
	if err != nil {
		return err
	}
	purposes := []string{"email_change"}
	if row.OriginalEmail != nil && *row.OriginalEmail != "" {
		purposes = append(purposes, "email_change_notice")
	}
	for _, purpose := range purposes {
		variables := accountMailVariables(n.appName, purpose, link, support, data)
		variables["TokenID"] = strconv.FormatInt(row.ID, 10)
		d, err := n.manager.delivery.Enqueue(ctx, row.TenantID, channel.Request{Channel: "email", TemplateCode: "identity." + purpose,
			UserID: *row.UserID, Variables: variables})
		if err != nil {
			return err
		}
		if n.production {
			if err := n.channelAvailability(ctx, d, meta.RequestID); err != nil {
				return err
			}
		}
	}
	return nil
}

func emailChangeTemplate(code string) bool {
	return code == "identity.email_change" || code == "identity.email_change_notice"
}

func (m *notificationManager) emailChangeMessageToken(ctx context.Context, tenant int64, in channel.Request) (model.AuthToken, error) {
	unavailable := errors.New("email change notification unavailable")
	id, err := strconv.ParseInt(in.Variables["TokenID"], 10, 64)
	if err != nil || id <= 0 || in.Channel != "email" {
		return model.AuthToken{}, unavailable
	}
	store := &accountStore{newAuthStore(m.store.database)}
	row, found, err := store.TokenByID(ctx, tenant, id)
	if err != nil {
		return row, err
	}
	if !found || row.Purpose != "email_change" || row.UserID == nil || *row.UserID != in.UserID || row.NewEmail == nil {
		return row, unavailable
	}
	// A safety notice is a durable record of the request, even after verification or supersession.
	if in.TemplateCode == "identity.email_change_notice" {
		if row.OriginalEmail == nil || *row.OriginalEmail == "" || in.Variables["Link"] != "" {
			return row, unavailable
		}
		return row, nil
	}
	link, err := url.Parse(in.Variables["Link"])
	if err != nil {
		return row, unavailable
	}
	fragment, err := url.ParseQuery(link.Fragment)
	if err != nil || fragment.Get("token") == "" || row.TokenHash != refresh.Hash(fragment.Get("token")) || !row.Valid(m.now().UTC()) {
		return row, unavailable
	}
	user, found, err := store.UserForReauth(ctx, tenant, in.UserID)
	if err != nil {
		return row, err
	}
	if !found || user.Status != "active" || !sameAccountEmail(user.Email, row.OriginalEmail) {
		return row, unavailable
	}
	return row, nil
}

func sameAccountEmail(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (m *notificationManager) emailChangeRecipient(ctx context.Context, tenant int64, in channel.Request) (string, error) {
	row, err := m.emailChangeMessageToken(ctx, tenant, in)
	if err != nil {
		return "", err
	}
	if in.TemplateCode == "identity.email_change_notice" {
		return *row.OriginalEmail, nil
	}
	return *row.NewEmail, nil
}

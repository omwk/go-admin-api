package bootstrap

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
)

type accountNotifications struct {
	manager    *notificationManager
	production bool
}

func (n *accountNotifications) Queue(ctx context.Context, tenant, user int64, purpose string, id int64, link, requestID string) (int64, error) {
	variables := map[string]string{"TokenID": strconv.FormatInt(id, 10)}
	// Keyless local operation records the event without exposing the credential.
	if n.manager.sealer != nil {
		variables["Link"] = link
	}
	delivery, err := n.manager.delivery.Enqueue(ctx, tenant, channel.Request{Channel: "email", TemplateCode: "identity." + purpose, UserID: user,
		Variables: variables})
	if err == nil && n.production && purpose == "reset" {
		err = n.channelAvailability(ctx, delivery, requestID)
	}
	return delivery.ID, err
}

func (n *accountNotifications) Deliver(ctx context.Context, tenant, id int64) error {
	_, err := n.manager.delivery.Deliver(ctx, tenant, id)
	return err
}

func (m *notificationManager) prepareAccountMessage(ctx context.Context, tenant int64, in channel.Request) (channel.Request, error) {
	if in.TemplateCode != "identity.invite" && in.TemplateCode != "identity.reset" {
		return in, nil
	}
	id, err := strconv.ParseInt(in.Variables["TokenID"], 10, 64)
	if err != nil || id <= 0 {
		return in, errors.New("account notification unavailable")
	}
	link, err := url.Parse(in.Variables["Link"])
	if err != nil || link.Fragment == "" {
		return in, errors.New("account notification unavailable")
	}
	fragment, err := url.ParseQuery(link.Fragment)
	if err != nil || fragment.Get("token") == "" {
		return in, errors.New("account notification unavailable")
	}
	store := &accountStore{newAuthStore(m.store.database)}
	row, found, err := store.TokenByID(ctx, tenant, id)
	if err != nil {
		return in, err
	}
	if !found || !row.Valid(m.now().UTC()) || in.TemplateCode != "identity."+row.Purpose || row.TokenHash != refresh.Hash(fragment.Get("token")) {
		return in, errors.New("account notification unavailable")
	}
	recipient := row.CreatedBy
	if row.UserID != nil {
		recipient = *row.UserID
	}
	if recipient != in.UserID {
		return in, errors.New("account notification unavailable")
	}
	return in, nil
}

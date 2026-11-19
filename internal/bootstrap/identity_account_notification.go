package bootstrap

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
)

type accountNotifications struct {
	manager    *notificationManager
	production bool
	appName    string
}

func (n *accountNotifications) Queue(ctx context.Context, tenant, user int64, purpose string, id int64, link, requestID string) (int64, error) {
	data, err := n.messageData(ctx, tenant, user, id, purpose)
	if err != nil {
		return 0, err
	}
	support, err := setting.SupportEmail(ctx, &systemSettingStore{database: n.manager.store.database}, tenant)
	if err != nil {
		return 0, err
	}
	// Keyless local operation records the event without exposing the credential.
	if n.manager.sealer == nil {
		link = ""
	}
	variables := accountMailVariables(n.appName, purpose, link, support, data)
	variables["TokenID"] = strconv.FormatInt(id, 10)
	delivery, err := n.manager.delivery.Enqueue(ctx, tenant, channel.Request{Channel: "email", TemplateCode: "identity." + purpose, UserID: user,
		Variables: variables})
	if err == nil && n.production && (purpose == "reset" || purpose == "verify") {
		err = n.channelAvailability(ctx, delivery, requestID)
	}
	return delivery.ID, err
}

type accountMailData struct {
	Username   string
	Nickname   *string
	TenantName string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	NewEmail   string
	IP         string
	UserAgent  string
}

func (n *accountNotifications) messageData(ctx context.Context, tenant, user, id int64, purpose string) (accountMailData, error) {
	q, cancel := n.manager.store.database.WithContext(ctx)
	defer cancel()
	var data accountMailData
	err := q.Table("auth_token AS a").Select("u.username,u.nickname,t.name AS tenant_name,a.created_at,a.expires_at").
		Joins("JOIN sys_user AS u ON u.tenant_id=a.tenant_id AND u.id=?", user).
		Joins("JOIN tenant AS t ON t.id=a.tenant_id").
		Where("a.tenant_id=? AND a.id=? AND a.purpose=?", tenant, id, purpose).Take(&data).Error
	return data, notificationDBError(err)
}

func accountMailVariables(appName, purpose, link, support string, data accountMailData) map[string]string {
	if appName == "" {
		appName = "CommerceHub"
	}
	name := data.Username
	if data.Nickname != nil && *data.Nickname != "" {
		name = *data.Nickname
	}
	lifetime := data.ExpiresAt.Sub(data.CreatedAt).Hours()
	// The existing variable name is retained; the reset copy explicitly uses minutes.
	if purpose == "reset" {
		lifetime = data.ExpiresAt.Sub(data.CreatedAt).Minutes()
	}
	variables := map[string]string{"AppName": appName, "UserName": name, "TenantName": data.TenantName, "Link": link,
		"ExpiresInHours": strconv.FormatFloat(lifetime, 'f', -1, 64), "ExpiresAt": data.ExpiresAt.UTC().Format(time.RFC3339), "SupportEmail": support}
	if purpose == "email_change" || purpose == "email_change_notice" {
		variables["NewEmail"], variables["RequestedAt"] = data.NewEmail, data.CreatedAt.UTC().Format(time.RFC3339)
		variables["IP"], variables["UserAgent"] = data.IP, data.UserAgent
		if purpose == "email_change_notice" {
			variables["Link"] = ""
		}
	}
	return variables
}

func (n *accountNotifications) Deliver(ctx context.Context, tenant, id int64) error {
	_, err := n.manager.delivery.Deliver(ctx, tenant, id)
	return err
}

func (m *notificationManager) prepareAccountMessage(ctx context.Context, tenant int64, in channel.Request) (channel.Request, error) {
	if emailChangeTemplate(in.TemplateCode) {
		_, err := m.emailChangeMessageToken(ctx, tenant, in)
		return in, err
	}
	if in.TemplateCode != "identity.invite" && in.TemplateCode != "identity.reset" && in.TemplateCode != "identity.verify" {
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

func (n *accountNotifications) EmailAvailable(ctx context.Context, tenant int64) (bool, error) {
	providers, err := n.manager.store.Providers(ctx, tenant, "email")
	return len(providers) > 0, err
}

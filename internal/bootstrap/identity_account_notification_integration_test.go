package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

type accountMailCapture struct {
	messages []channel.Message
	fail     bool
}

func (*accountMailCapture) Channel() string            { return "email" }
func (*accountMailCapture) Kind() string               { return "smtp" }
func (*accountMailCapture) Test(context.Context) error { return nil }
func (s *accountMailCapture) Send(_ context.Context, m channel.Message) error {
	s.messages = append(s.messages, m)
	if s.fail {
		return errors.New("mail transport unavailable")
	}
	return nil
}

func accountMailProvider(t *testing.T, f *accountFixture) *accountMailCapture {
	t.Helper()
	email := strings.ToLower(accountPassword(t)) + "@example.invalid"
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET email=? WHERE id=?", email, f.user.ID))
	_, err := f.notifications.SaveProvider(f.ctx, channel.Actor{TenantID: 1, UserID: f.user.ID, Super: true}, 0,
		channel.ProviderInput{Channel: "email", Kind: "smtp", Name: "account-mail", Enabled: true,
			Config: channel.Config{"smtp_addr": "127.0.0.1:25", "from_address": email, "tls_mode": "plain"}})
	require.NoError(t, err)
	capture := &accountMailCapture{}
	f.notifications.delivery.Factory = func(string, string, channel.Config) (channel.Sender, error) { return capture, nil }
	return capture
}

func accountOutbox(t *testing.T, f *accountFixture, code string) channel.Outbox {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var out channel.Outbox
	require.NoError(t, q.Table("notify_outbox").Select("id,tenant_id,channel,template_code,payload,status").Where("template_code=?", code).Order("id DESC").Take(&out).Error)
	return out
}

func assertAccountEncryptedLink(t *testing.T, f *accountFixture, out channel.Outbox, link string) {
	t.Helper()
	var request channel.Request
	require.NoError(t, channel.OpenJSON(f.notifications.sealer, fmt.Sprintf("notification-outbox:v1:1:%d", out.ID), out.Payload, &request))
	require.Len(t, request.Variables, 2)
	require.NotEmpty(t, request.Variables["TokenID"])
	require.True(t, link == request.Variables["Link"])
	require.NotContains(t, string(out.Payload), inviteRaw(t, link))
	require.NotContains(t, string(out.Payload), link)
}

func TestIdentityAccountNotificationIntegrationUnavailableAndDigestOnly(t *testing.T) {
	// F1 §5.4: an absent channel leaves an outbox record and a usable administrator link.
	f := newAccountFixture(t)
	invite := f.invite(t, 1)
	raw := inviteRaw(t, invite.Link)
	out := accountOutbox(t, f, "identity.invite")
	require.Equal(t, "channel_unavailable", out.Status)
	assertAccountEncryptedLink(t, f, out, invite.Link)
	_, err := f.accounts.AcceptInvite(f.ctx, 1, identity.AcceptInviteInput{Token: raw, Username: "copy-member", Password: accountPassword(t)}, identity.RequestMeta{})
	require.NoError(t, err)
	reset, err := f.accounts.ForgotPassword(f.ctx, 1, f.user.Username, identity.RequestMeta{})
	require.NoError(t, err)
	raw = inviteRaw(t, reset.Link)
	out = accountOutbox(t, f, "identity.reset")
	d, err := f.notifications.delivery.Deliver(f.ctx, 1, out.ID)
	require.NoError(t, err)
	require.Equal(t, "channel_unavailable", d.LastError)
	assertAccountEncryptedLink(t, f, out, reset.Link)
	require.NoError(t, f.accounts.ResetPassword(f.ctx, 1, raw, accountPassword(t), identity.RequestMeta{}))
}

func TestIdentityAccountNotificationIntegrationMailLinksAndConsumedRetry(t *testing.T) {
	f := newAccountFixture(t)
	capture := accountMailProvider(t, f)
	invite := f.invite(t, 1)
	require.Len(t, capture.messages, 1)
	require.Contains(t, capture.messages[0].Body, invite.Link)
	require.True(t, strings.HasPrefix(invite.Link, f.accounts.PublicURL+"/auth/invite/accept?tenant_id=1#token="))
	_, err := f.accounts.AcceptInvite(f.ctx, 1, identity.AcceptInviteInput{Token: inviteRaw(t, invite.Link), Username: "mail-member", Password: accountPassword(t)}, identity.RequestMeta{})
	require.NoError(t, err)
	reset, err := f.accounts.ForgotPassword(f.ctx, 1, f.user.Username, identity.RequestMeta{})
	require.NoError(t, err)
	require.Len(t, capture.messages, 1) // Forgot queues without synchronous provider-dependent work.
	out := accountOutbox(t, f, "identity.reset")
	assertAccountEncryptedLink(t, f, out, reset.Link)
	d, err := f.notifications.delivery.Deliver(f.ctx, 1, out.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	require.Len(t, capture.messages, 2)
	link := capture.messages[1].Variables["Link"]
	raw := inviteRaw(t, link)
	require.True(t, reset.Link == link)
	stored, found, err := f.accounts.Store.TokenByHash(f.ctx, 1, "reset", refresh.Hash(raw))
	require.NoError(t, err)
	require.True(t, found)
	require.NoError(t, f.accounts.ResetPassword(f.ctx, 1, raw, accountPassword(t), identity.RequestMeta{}))
	_, err = f.notifications.prepareAccountMessage(f.ctx, 1, channel.Request{TemplateCode: "identity.reset", UserID: f.user.ID,
		Variables: map[string]string{"TokenID": fmt.Sprint(stored.ID), "Link": reset.Link}})
	require.Error(t, err)
	require.Len(t, capture.messages, 2)
	assertAccountEncryptedLink(t, f, out, reset.Link)
}

func TestIdentityAccountNotificationIntegrationLockedRecoveryAndDisabledPrivacy(t *testing.T) {
	f := newAccountFixture(t)
	capture := accountMailProvider(t, f)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET status='locked',failed_attempts=5,locked_until=? WHERE id=?", f.now.Add(time.Minute), f.user.ID))
	_, err := f.accounts.ForgotPassword(f.ctx, 1, f.user.Username, identity.RequestMeta{})
	require.NoError(t, err)
	out := accountOutbox(t, f, "identity.reset")
	delivery, err := f.notifications.delivery.Deliver(f.ctx, 1, out.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", delivery.Status)
	require.Len(t, capture.messages, 1)
	password := accountPassword(t)
	require.NoError(t, f.accounts.ResetPassword(f.ctx, 1, inviteRaw(t, capture.messages[0].Variables["Link"]), password, identity.RequestMeta{}))
	_, err = f.service.Login(f.ctx, 1, f.user.Username, password, identity.RequestMeta{})
	require.NoError(t, err)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET status='disabled' WHERE id=?", f.user.ID))
	result, err := f.accounts.ForgotPassword(f.ctx, 1, f.user.Username, identity.RequestMeta{})
	require.NoError(t, err)
	require.ErrorContains(t, f.accounts.ResetPassword(f.ctx, 1, inviteRaw(t, result.Link), accountPassword(t), identity.RequestMeta{}), "邀请或链接无效")
	require.Len(t, capture.messages, 1)
}

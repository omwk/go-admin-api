package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func TestIdentityRegistrationIntegrationMailFallbackAndDelivery(t *testing.T) {
	f := newRegistrationFixture(t)
	f.settings(t, map[string]any{"allow_self_registration": true})
	dev := registrationInput(t, "dev-without-channel")
	result := f.register(t, dev, 201)
	require.NotEmpty(t, result.Link)
	require.True(t, strings.Contains(result.Link, "/auth/register/verify?tenant_id=1#token="))
	fallback := accountOutbox(t, f.accountFixture, "identity.verify")
	assertAccountEncryptedLink(t, f.accountFixture, fallback, result.Link)
	f.accounts.CopyResetLinks = false
	f.accounts.Notify.(*accountNotifications).production = true
	production := registrationInput(t, "prod-without-channel")
	prod := f.register(t, production, 202)
	require.Empty(t, prod.Link)
	again := f.register(t, production, 202)
	require.Empty(t, again.Link)
	unavailable := accountOutbox(t, f.accountFixture, "identity.verify")
	require.Equal(t, "channel_unavailable", unavailable.Status)
	require.NotContains(t, string(unavailable.Payload), production.Email)
	require.NotContains(t, string(unavailable.Payload), production.Password)
	capture := accountMailProvider(t, f.accountFixture)
	configured := registrationInput(t, "prod-configured")
	created := f.register(t, configured, 201)
	require.Empty(t, created.Link)
	require.Empty(t, capture.messages)
	queued := accountOutbox(t, f.accountFixture, "identity.verify")
	delivery, err := f.notifications.delivery.Deliver(f.ctx, 1, queued.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", delivery.Status)
	require.Len(t, capture.messages, 1)
	require.True(t, capture.messages[0].Recipient == configured.Email)
	link := capture.messages[0].Variables["Link"]
	require.NotEmpty(t, link)
	assertAccountEncryptedLink(t, f.accountFixture, queued, link)
	f.verify(t, link, 204)
	_, err = f.notifications.prepareAccountMessage(f.ctx, 1, channel.Request{TemplateCode: "identity.verify",
		UserID: registrationUser(t, f, 1, configured.Account).ID, Variables: capture.messages[0].Variables})
	require.Error(t, err)
	t.Log("§4 #13: dev missing channel returns usable fragment link; prod missing channel returns only accepted/202 plus F3 unavailable outbox; configured prod queues 201 then sends to disabled pending user; consumed-link retry rejected")
}

func TestIdentityRegistrationIntegrationAuditAndSensitiveData(t *testing.T) {
	f := newRegistrationFixture(t)
	f.settings(t, map[string]any{"allow_self_registration": true})
	in := registrationInput(t, "audited-registration")
	out := f.register(t, in, 201)
	f.verify(t, out.Link, 204)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var events []struct {
		Action string
		Before json.RawMessage
		After  json.RawMessage
	}
	require.NoError(t, q.Table("audit_log").Select("action,\"before\",\"after\"").
		Where("tenant_id=? AND action IN ?", 1, []string{"system:setting:update", "identity.register", "identity.register.verify"}).
		Order("id").Find(&events).Error)
	require.Len(t, events, 3)
	require.Equal(t, "system:setting:update", events[0].Action)
	require.Equal(t, "identity.register", events[1].Action)
	require.Equal(t, "identity.register.verify", events[2].Action)
	raw := inviteRaw(t, out.Link)
	for _, event := range events {
		data := string(event.Before) + string(event.After)
		for _, sensitive := range []string{in.Password, in.Email, raw, out.Link} {
			require.False(t, strings.Contains(data, sensitive), "audit must contain safe metadata only")
		}
	}
	require.Contains(t, string(events[0].After), "digest")
	require.Contains(t, string(events[2].After), "active")
	t.Log("§4 #12: registration, verification and switch change each audited atomically; snapshots exclude password/email/raw credential/link")
}

package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func notificationFixture(t *testing.T) (*credentialRefreshFixture, *credentialNotifications, credentialAlert) {
	t.Helper()
	f, r := wiredCredentialFixture(t)
	n := newCredentialNotifications(r)
	a := credentialAlert{TenantID: 1, CredentialID: f.target.CredentialID, ShopID: f.shop.ID, OwnerUserID: f.actor.UserID, Platform: "shopee", Template: "credential.needs_reauth", Severity: "critical"}
	return f, n, a
}

func notificationCount(t *testing.T, f *credentialRefreshFixture) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("notification").Where("credential_id=?", f.target.CredentialID).Count(&count).Error)
	return count
}

// REQ-SHP-009: rolling dedup persists across midnight, restarts and concurrent observers.
func TestCredentialNotificationIntegrationRollingDedup(t *testing.T) {
	f, n, a := notificationFixture(t)
	f.now = time.Date(2026, 11, 9, 23, 59, 0, 0, time.UTC)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- n.notify(f.ctx, a) }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, notificationCount(t, f))
	f.now = f.now.Add(23*time.Hour + 59*time.Minute)
	require.NoError(t, newCredentialNotifications(n.runtime).notify(f.ctx, a))
	require.EqualValues(t, 1, notificationCount(t, f))
	f.now = f.now.Add(time.Minute)
	require.NoError(t, n.notify(f.ctx, a))
	require.EqualValues(t, 2, notificationCount(t, f))
	require.NoError(t, n.Read(f.ctx, 1, f.actor.UserID, 1))
	require.Error(t, n.Read(f.ctx, 2, f.actor.UserID, 1))
	f.assertSafe(t)
}

func TestCredentialNotificationIntegrationSeverityUpgrade(t *testing.T) {
	f, n, a := notificationFixture(t)
	a.Template, a.Severity = "credential.stuck", "warning"
	require.NoError(t, n.notify(f.ctx, a))
	require.NoError(t, n.notify(f.ctx, a))
	require.EqualValues(t, 1, notificationCount(t, f))
	a.Severity = "critical"
	require.NoError(t, n.notify(f.ctx, a))
	require.NoError(t, n.notify(f.ctx, a))
	require.EqualValues(t, 2, notificationCount(t, f))
}

func TestCredentialNotificationIntegrationChannelsRetryAndRedaction(t *testing.T) {
	f, n, a := notificationFixture(t)
	input := notifier.Settings{EmailEnabled: true, WebhookEnabled: true, SMTPAddr: "127.0.0.1:1", SMTPFrom: "maintenance@example.invalid", SMTPPassword: f.randomValue(t), WebhookURL: "https://example.invalid/notify", WebhookSecret: f.randomValue(t)}
	require.NoError(t, n.SaveSettings(f.ctx, 1, f.actor.UserID, input))
	require.NoError(t, n.notify(f.ctx, a))
	require.NoError(t, n.notify(f.ctx, a))
	require.EqualValues(t, 3, notificationCount(t, f))
	calls := map[string]int{}
	n.send = func(_ context.Context, channel string, _ notifier.Settings, _ string, m notifier.Message) error {
		calls[channel]++
		require.NoError(t, m.Validate())
		if channel == "email" {
			return errors.New("upstream private response")
		}
		return nil
	}
	for i := 0; i < 2; i++ {
		more, err := n.deliverOne(f.ctx)
		require.NoError(t, err)
		require.True(t, more)
	}
	for i := 0; i < 3; i++ {
		f.now = f.now.Add(10 * time.Minute)
		_, err := n.deliverOne(f.ctx)
		require.NoError(t, err)
	}
	require.Equal(t, 3, calls["email"])
	require.Equal(t, 1, calls["webhook"])
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []credentialNotification
	require.NoError(t, q.Table("notification").Select("channel,status,attempts,last_error").Where("credential_id=?", a.CredentialID).Order("id").Find(&rows).Error)
	require.Equal(t, "sent", rows[0].Status)
	require.Equal(t, "failed", rows[1].Status)
	require.Equal(t, "sent", rows[2].Status)
	require.NotContains(t, rows[1].LastError, "private")
	settings, err := n.Settings(f.ctx, 1, f.actor.UserID)
	require.NoError(t, err)
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	f.noLeaks(t, "settings", string(raw))
	f.assertSafe(t)
}

func TestCredentialNotificationIntegrationRevokedRecipientAndHTTP(t *testing.T) {
	f, n, a := notificationFixture(t)
	require.NoError(t, n.SaveSettings(f.ctx, 1, f.actor.UserID, notifier.Settings{WebhookEnabled: true, WebhookURL: "https://example.invalid/notify"}))
	require.NoError(t, n.notify(f.ctx, a))
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, registerCredentialNotificationRoutes(f.router, f.identityFixture.service, profiles, n.runtime))
	q, finish, err := n.inboxQuery(f.ctx, 1, f.actor.UserID)
	defer finish()
	require.NoError(t, err)
	var rows []credentialNotification
	require.NoError(t, q.Select("n.id,n.channel,n.template,n.payload,n.status,n.attempts,n.last_error,n.read_at,n.created_at,n.credential_id").Find(&rows).Error)
	response, _ := f.request(t, http.MethodGet, "/notifications", nil)
	require.Equal(t, 200, response.Code)
	response, _ = f.request(t, http.MethodPut, "/notifications/1/read", map[string]any{})
	require.Equal(t, 200, response.Code)
	response, _ = f.request(t, http.MethodGet, "/notification-settings", nil)
	require.Equal(t, 200, response.Code)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET status='disabled' WHERE id=?", f.actor.UserID))
	n.send = func(context.Context, string, notifier.Settings, string, notifier.Message) error {
		t.Fatal("revoked recipient received a notification")
		return nil
	}
	more, err := n.deliverOne(f.ctx)
	require.NoError(t, err)
	require.True(t, more)
}

func TestCredentialNotificationIntegrationActualWebhookDelivery(t *testing.T) {
	f, n, a := notificationFixture(t)
	var received notifier.Message
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.WriteHeader(204)
	}))
	defer server.Close()
	require.NoError(t, n.SaveSettings(f.ctx, 1, f.actor.UserID, notifier.Settings{WebhookEnabled: true, WebhookURL: server.URL}))
	require.NoError(t, n.notify(f.ctx, a))
	more, err := n.deliverOne(f.ctx)
	require.NoError(t, err)
	require.True(t, more)
	require.Equal(t, a.Template, received.Template)
	require.Positive(t, received.EventID)
	f.assertSafe(t)
}

func TestCredentialNotificationIntegrationRecoveryAndPlatformAggregation(t *testing.T) {
	f, n, a := notificationFixture(t)
	require.NoError(t, n.SaveSettings(f.ctx, 1, f.actor.UserID, notifier.Settings{WebhookEnabled: true, WebhookURL: "https://example.invalid/notify"}))
	a.Template, a.Severity = "credential.failed", "warning"
	require.NoError(t, n.notify(f.ctx, a))
	other, _ := f.create(t, "oauth2")
	a.CredentialID = other.ID
	require.NoError(t, n.notify(f.ctx, a))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var counts struct{ Inbox, Webhook int64 }
	require.NoError(t, q.Table("notification").Select("count(*) FILTER(WHERE channel='inbox') AS inbox,count(*) FILTER(WHERE channel='webhook') AS webhook").Scan(&counts).Error)
	require.EqualValues(t, 2, counts.Inbox)
	require.EqualValues(t, 1, counts.Webhook)
	payload, err := json.Marshal(map[string]int64{"credential_id": f.target.CredentialID, "version_id": f.target.InputVersionID})
	require.NoError(t, err)
	event := outbox.Event{TenantID: 1, AggregateType: "credential", AggregateID: strconv.FormatInt(f.target.CredentialID, 10), Payload: payload}
	require.NoError(t, n.recovered(f.ctx, event))
	require.NoError(t, n.recovered(f.ctx, event))
	var recovered int64
	require.NoError(t, q.Table("notification").Where("template='credential.recovered' AND channel='inbox'").Count(&recovered).Error)
	require.EqualValues(t, 1, recovered)
}

func TestCredentialNotificationIntegrationTransferHidesOldInboxAndStopsDelivery(t *testing.T) {
	f, n, a := notificationFixture(t)
	require.NoError(t, n.SaveSettings(f.ctx, 1, f.actor.UserID, notifier.Settings{WebhookEnabled: true, WebhookURL: "https://example.invalid/notify"}))
	require.NoError(t, n.notify(f.ctx, a))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var newOwner int64
	require.NoError(t, q.Raw("INSERT INTO sys_user(tenant_id,username,password_hash,dept_id,status) SELECT tenant_id,'notification-new-owner',password_hash,dept_id,'active' FROM sys_user WHERE id=? RETURNING id", f.actor.UserID).Scan(&newOwner).Error)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop SET owner_user_id=? WHERE id=?", newOwner, f.shop.ID))
	inbox, finish, err := n.inboxQuery(f.ctx, 1, f.actor.UserID)
	defer finish()
	require.NoError(t, err)
	var count int64
	require.NoError(t, inbox.Count(&count).Error)
	require.Zero(t, count)
	require.Error(t, n.Read(f.ctx, 1, f.actor.UserID, 1))
	n.send = func(context.Context, string, notifier.Settings, string, notifier.Message) error {
		t.Error("old owner received notification after transfer")
		return nil
	}
	_, err = n.deliverOne(f.ctx)
	require.NoError(t, err)
}

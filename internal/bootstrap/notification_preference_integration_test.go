package bootstrap

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func s3aPreference(t *testing.T, data map[string]any, template, kind string) notifier.PreferenceView {
	t.Helper()
	raw, err := json.Marshal(data["preferences"])
	require.NoError(t, err)
	var rows []notifier.PreferenceView
	require.NoError(t, json.Unmarshal(raw, &rows))
	for _, row := range rows {
		if row.Template == template && row.Channel == kind {
			return row
		}
	}
	t.Fatalf("missing preference %s/%s", template, kind)
	return notifier.PreferenceView{}
}

func s3aPreferenceInput(template, kind string, enabled bool) map[string]any {
	return map[string]any{"template": template, "channel": kind, "enabled": enabled}
}

func TestSystemNotificationPreferencesIntegrationS3a(t *testing.T) {
	// S3a §4.5/7: defaults are live, upserts are actor-scoped and invalid batches are atomic.
	f := newS3aFixture(t)
	get := func() map[string]any {
		return s3aData(t, s3aRequest(t, f, http.MethodGet, "/notifications/preferences", "", nil))
	}
	before := s3aPreference(t, get(), "notification.default", "email")
	require.False(t, before.Enabled)
	require.True(t, before.Inherited)
	sender := hex.EncodeToString(f.secret[:8]) + "@example.invalid"
	require.NoError(t, f.notifications.SaveSettings(f.ctx, 1, f.actor.User.ID, notifier.Settings{EmailEnabled: true, SMTPAddr: "127.0.0.1:1", SMTPFrom: sender}))
	before = s3aPreference(t, get(), "notification.default", "email")
	require.True(t, before.Enabled)
	require.True(t, before.Inherited)
	input := map[string]any{"preferences": []any{s3aPreferenceInput("notification.default", "email", false)}, "user_id": f.user.ID, "tenant_id": 2}
	for run := 0; run < 2; run++ {
		w := s3aRequest(t, f, http.MethodPut, "/notifications/preferences", "preferences", input)
		view := s3aPreference(t, s3aData(t, w), "notification.default", "email")
		require.False(t, view.Enabled)
		require.False(t, view.Inherited)
	}
	others, err := f.notifications.Preferences(f.ctx, 1, f.user.ID)
	require.NoError(t, err)
	for _, row := range others {
		require.True(t, row.Inherited)
		if row.Channel == "email" {
			require.True(t, row.Enabled)
		}
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("notification_preference").Where("tenant_id=1 AND user_id=?", f.actor.User.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, q.Table("audit_log").Where("action='notification.preferences.update'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'s3a-foreign','s3a-foreign')`))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO notify_template(tenant_id,code,subject,body,format) VALUES(2,'foreign.template','safe','safe','text')`))
	s3aInvalidPreferenceBatches(t, f)
	view := s3aPreference(t, get(), "notification.default", "email")
	require.False(t, view.Enabled, "invalid batch must not partially enable email")
	t.Log("email inherits tenant false -> true; explicit false is private to actor; replay stores/audits once; unknown/foreign template and invalid batches=400 atomically")
}

func s3aInvalidPreferenceBatches(t *testing.T, f *s3aFixture) {
	t.Helper()
	valid := s3aPreferenceInput("notification.default", "email", true)
	for _, batch := range [][]any{
		{valid, s3aPreferenceInput("unknown", "email", false)},
		{s3aPreferenceInput("foreign.template", "email", false)},
		{s3aPreferenceInput("notification.default", "inbox", false)},
		{valid, valid},
		{map[string]any{"template": "notification.default", "channel": "email"}},
		{},
	} {
		w := s3aRequest(t, f, http.MethodPut, "/notifications/preferences", "", map[string]any{"preferences": batch})
		require.Equal(t, 400, w.Code, w.Body.String())
	}
}

func TestSystemNotificationLegacyPreferenceIntegrationS3a(t *testing.T) {
	// S3a §4.6: an email opt-out never prevents the inbox transaction.
	f, n, alert := notificationFixture(t)
	sender := hex.EncodeToString(f.secret[:8]) + "@example.invalid"
	require.NoError(t, n.SaveSettings(f.ctx, 1, f.actor.UserID, notifier.Settings{EmailEnabled: true, SMTPAddr: "127.0.0.1:1", SMTPFrom: sender}))
	disabled := false
	_, err := n.SavePreferences(f.ctx, 1, f.actor.UserID, []notifier.Preference{{Template: alert.Template, Channel: "email", Enabled: &disabled}})
	require.NoError(t, err)
	calls := 0
	n.send = func(context.Context, string, notifier.Settings, string, notifier.Message) error { calls++; return nil }
	require.NoError(t, n.notify(f.ctx, alert))
	require.EqualValues(t, 1, notificationCount(t, f))
	more, err := n.deliverOne(f.ctx)
	require.NoError(t, err)
	require.False(t, more)
	require.Zero(t, calls)
	count, err := n.UnreadCount(f.ctx, 1, f.actor.UserID)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	// Other templates still inherit the tenant's enabled email channel.
	alert.Template = "credential.expired"
	require.NoError(t, n.notify(f.ctx, alert))
	more, err = n.deliverOne(f.ctx)
	require.NoError(t, err)
	require.True(t, more)
	require.Equal(t, 1, calls)
	f.assertSafe(t)
	t.Log("disabled template: inbox=1/unread=1, email calls=0; other template inherits tenant email and sends")
}

func TestSystemNotificationManagedPreferenceIntegrationS3a(t *testing.T) {
	// S3a §4.6: F3 queues and already queued deliveries both honor an opt-out.
	f := newNotificationFixtureF3(t)
	addr, calls := localSMTPF3(t)
	p := f.provider(t, "smtp", "preference", 0, true, channel.Config{"smtp_addr": addr, "from_address": f.email})
	n := newCredentialNotifications(&credentialRefreshRuntime{database: f.database, sealer: f.manager.sealer, now: f.manager.now})
	request := channel.Request{Channel: "email", TemplateCode: "notification.default", UserID: f.user.ID, Variables: map[string]string{"Name": "Ada"}}
	d, err := f.manager.delivery.Enqueue(f.ctx, 1, request)
	require.NoError(t, err)
	require.Positive(t, d.ID)
	disabled := false
	_, err = n.SavePreferences(f.ctx, 1, f.user.ID, []notifier.Preference{{Template: request.TemplateCode, Channel: "email", Enabled: &disabled}})
	require.NoError(t, err)
	blocked, err := f.manager.delivery.Send(f.ctx, 1, request)
	require.NoError(t, err)
	require.Zero(t, blocked.ID)
	require.Equal(t, "preference_disabled", blocked.LastError)
	d, err = f.manager.delivery.Deliver(f.ctx, 1, d.ID)
	require.NoError(t, err)
	require.Equal(t, "skipped", d.Status)
	require.Equal(t, "preference_disabled", d.LastError)
	require.Zero(t, d.Attempts)
	require.Zero(t, calls.Load())
	w := f.request(t, http.MethodPost, "/providers/"+fmtNotificationID(p.ID)+"/test", "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.EqualValues(t, 1, calls.Load(), "explicit provider tests remain available")
	f.assertPrivacy(t)
	t.Log("F3 opt-out: no new outbox; pending email skipped before transport; provider test still succeeds")
}

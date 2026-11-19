package bootstrap

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func TestAdminNotificationPreferencesIntegrationComplete(t *testing.T) {
	// BE-IDN-018 `3 #1/2: labels and immutable security preferences are actor scoped.
	f := newS3aFixture(t)
	require.NoError(t, seedNotificationTemplates(f.ctx, f.database))
	w := s3aRequest(t, f, http.MethodGet, "/notifications/preferences", "", nil)
	view := s3aPreference(t, s3aData(t, w), "identity.verify", "email")
	require.Equal(t, "验证注册邮箱", view.Name)
	require.True(t, view.Transactional)
	require.True(t, view.Enabled)
	for _, code := range []string{"identity.verify", "identity.reset", "identity.invite"} {
		w = s3aRequest(t, f, http.MethodPut, "/notifications/preferences", "", map[string]any{"preferences": []any{s3aPreferenceInput(code, "email", false)}})
		require.Equal(t, 409, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "安全邮件不可关闭")
	}
	w = s3aRequest(t, f, http.MethodPut, "/notifications/preferences", "", map[string]any{"preferences": []any{s3aPreferenceInput("notification.default", "email", false)}})
	view = s3aPreference(t, s3aData(t, w), "notification.default", "email")
	require.False(t, view.Enabled)
	require.False(t, view.Transactional)
	view = s3aPreference(t, s3aData(t, s3aRequest(t, f, http.MethodGet, "/notifications/preferences", "", nil)), "notification.default", "email")
	require.False(t, view.Enabled)
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO notification_preference(tenant_id,user_id,template,channel,enabled) VALUES(1,?,'identity.verify','email',false)`, f.actor.User.ID))
	allowed, err := notificationPreferenceAllowed(f.ctx, f.database, 1, f.actor.User.ID, "identity.verify", "email")
	require.NoError(t, err)
	require.True(t, allowed)
	t.Log("`3 #1/#2: name=验证注册邮箱; three transactional opt-outs409; ordinary false persists; legacy security opt-out cannot suppress queue")
}

func TestAdminNotificationTemplateToolsIntegrationComplete(t *testing.T) {
	// BE-IDN-018 `3 #3-7: examples, named errors, exact-ID preview and actual SMTP message.
	f := newNotificationFixtureF3(t)
	require.NoError(t, seedNotificationTemplates(f.ctx, f.database))
	w := f.request(t, http.MethodGet, "/templates/variables", "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var variables map[string][]channel.Variable
	notificationDataF3(t, w, &variables)
	require.Len(t, variables, 15)
	for _, tpl := range seededNotificationTemplates() {
		require.Equal(t, channel.Variables(tpl.Code), variables[tpl.Code])
		require.NoError(t, channel.ValidateTemplate(tpl))
	}
	tpl := channel.Template{Locale: "zh-CN", Format: "text", Enabled: true, Subject: "safe", Body: "{{.UndeclaredForCode}}"}
	w = f.request(t, http.MethodPut, "/templates/identity.verify", "", tpl)
	require.Equal(t, 400, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "UndeclaredForCode")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var id int64
	require.NoError(t, q.Table("notify_template").Select("id").Where("code='identity.verify' AND tenant_id IS NULL").Take(&id).Error)
	path := "/templates/" + fmtNotificationID(id)
	w = f.request(t, http.MethodPost, path+"/preview", "preview", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var preview channel.TemplatePreview
	notificationDataF3(t, w, &preview)
	require.NotContains(t, preview.Subject+preview.Body, "{{")
	require.Contains(t, preview.Subject, "CommerceHub")
	addr, messages := completeSMTP(t)
	recipient := "test" + hex.EncodeToString(f.secret[:8]) + "@example.invalid"
	f.provider(t, "smtp", "template-test", 0, true, channel.Config{"smtp_addr": addr, "from_address": f.email, "test_recipient": recipient})
	for i := 0; i < 2; i++ {
		w = f.request(t, http.MethodPost, path+"/test", "template-test", nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		var delivery channel.Delivery
		notificationDataF3(t, w, &delivery)
		require.Positive(t, delivery.ID)
		require.Equal(t, "sent", delivery.Status)
		require.NotNil(t, delivery.RecipientMasked)
	}
	msg := completeReceive(t, messages)
	require.Equal(t, recipient, msg.Recipient)
	require.Contains(t, strings.ReplaceAll(msg.Body, "\r\n", "\n"), preview.Body)
	select {
	case <-messages:
		t.Fatal("idempotent template test sent twice")
	default:
	}
	d, err := f.manager.delivery.Enqueue(f.ctx, 1, channel.Request{Channel: "email", TemplateCode: "notification.default", UserID: f.user.ID, Variables: map[string]string{"Name": "Ada"}})
	require.NoError(t, err)
	require.Equal(t, "pending", d.Status)
	require.NotNil(t, d.RecipientMasked)
	stored, err := f.manager.deliveryByID(f.ctx, 1, d.ID)
	require.NoError(t, err)
	require.Equal(t, d.RecipientMasked, stored.RecipientMasked)
	changed := "changed" + hex.EncodeToString(f.secret[8:16]) + "@example.invalid"
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET email=? WHERE id=?", changed, f.user.ID))
	d, err = f.manager.delivery.Deliver(f.ctx, 1, d.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	require.Equal(t, changed, completeReceive(t, messages).Recipient, "dispatch re-resolves actual recipient")
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'complete-foreign','foreign')`))
	var foreign channel.Template
	foreign = channel.Template{TenantID: ptrComplete(int64(2)), Code: "foreign", Locale: "zh-CN", Format: "text", Subject: "safe", Body: "safe", Enabled: true}
	require.NoError(t, q.Table("notify_template").Create(&foreign).Error)
	require.Equal(t, 404, f.request(t, http.MethodPost, "/templates/"+fmtNotificationID(foreign.ID)+"/preview", "", nil).Code)
	_, err = f.manager.templateByID(f.ctx, 2, id+100000)
	require.Equal(t, 404, errs.From(err).HTTPStatus)
	f.assertPrivacy(t, recipient, changed, f.token)
	t.Log("`3 #3-7: 15 declared codes; unknown variable named400; examples preview; SMTP received exact template at configured test recipient once; pending mask persisted; dispatch uses updated destination; foreign ID404")
}

func ptrComplete[T any](v T) *T { return &v }

func TestAdminNotificationTemplateDeclarationsMatchProducers(t *testing.T) {
	for _, tpl := range seededNotificationTemplates() {
		vars := seededTemplateVariables(t, tpl.Code, "support@example.invalid")
		delete(vars, "TokenID")
		require.ElementsMatch(t, mapKeysComplete(vars), mapKeysComplete(channel.ExampleVariables(tpl.Code)), tpl.Code)
	}
}

func mapKeysComplete(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestAdminNotificationInboxContentIntegrationComplete(t *testing.T) {
	// BE-IDN-018 `3 #13: legacy rows remain readable and summaries count Unicode characters.
	f := newS3aFixture(t)
	require.NoError(t, seedNotificationTemplates(f.ctx, f.database))
	payload, err := json.Marshal(map[string]string{"Platform": "Example", "Severity": "critical", "Path": "/admin/notifications"})
	require.NoError(t, err)
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO notification(tenant_id,channel,target,template,payload,status) VALUES(1,'inbox',?,'credential.expired',?,'sent')`, "user:"+fmtNotificationID(f.actor.User.ID), payload))
	w := s3aRequest(t, f, http.MethodGet, "/notifications?template=credential.expired", "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var response struct {
		Data struct{ List []credentialNotification }
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.Data.List, 1)
	row := response.Data.List[0]
	require.Equal(t, "凭据已过期", row.Title)
	require.Equal(t, "凭据已过期", row.TemplateName)
	require.NotEmpty(t, row.Summary)
	body := strings.Repeat("系统维护", 40)
	payload, err = json.Marshal(map[string]string{"title": "维护通知", "body": body})
	require.NoError(t, err)
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO notification(tenant_id,channel,target,template,payload,status) VALUES(1,'inbox',?,'notification.broadcast',?,'sent')`, "user:"+fmtNotificationID(f.actor.User.ID), payload))
	w = s3aRequest(t, f, http.MethodGet, "/notifications?template=notification.broadcast", "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.Data.List, 1)
	require.Equal(t, "维护通知", response.Data.List[0].Title)
	require.Equal(t, strings.Repeat("系统维护", 30), response.Data.List[0].Summary)
	_, err = f.notifications.Inbox(f.ctx, 1, f.actor.User.ID, 1, 20, notifier.InboxFilter{})
	require.NoError(t, err)
	t.Log("`3 #13: legacy title falls back to template name; template filter returns template and template_name; Unicode summary is exactly120 characters")
}

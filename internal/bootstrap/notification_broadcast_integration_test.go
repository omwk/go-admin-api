package bootstrap

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

type completeBroadcastFixture struct {
	*notificationFixtureF3
	alice, bob, foreign, disabled model.User
	roles                         []int64
	foreignRole                   int64
}

func newCompleteBroadcastFixture(t *testing.T) *completeBroadcastFixture {
	t.Helper()
	f := &completeBroadcastFixture{notificationFixtureF3: newNotificationFixtureF3(t)}
	require.NoError(t, seedNotificationTemplates(f.ctx, f.database))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'broadcast-foreign','foreign')`))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	dept := model.Department{TenantID: 2, Name: "foreign", Path: "foreign", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	for i, u := range []*model.User{&f.alice, &f.bob, &f.foreign, &f.disabled} {
		*u = model.User{TenantID: 1, Username: "recipient" + fmtNotificationID(int64(i)), DepartmentID: f.user.DepartmentID, Status: "active", PasswordHash: f.user.PasswordHash}
		if i == 2 {
			u.TenantID, u.DepartmentID = 2, dept.ID
		}
		if i == 3 {
			u.Status = "disabled"
		}
		email := u.Username + f.email
		u.Email = &email
		require.NoError(t, q.Create(u).Error)
	}
	for i := 0; i < 3; i++ {
		role := model.Role{TenantID: 1, Code: "audience" + fmtNotificationID(int64(i)), Name: "audience", DataScope: "self", Status: "active"}
		if i == 2 {
			role.TenantID = 2
		}
		require.NoError(t, q.Create(&role).Error)
		if i == 2 {
			f.foreignRole = role.ID
			continue
		}
		f.roles = append(f.roles, role.ID)
	}
	for _, grant := range [][2]int64{{f.alice.ID, f.roles[0]}, {f.alice.ID, f.roles[1]}, {f.bob.ID, f.roles[1]}, {f.disabled.ID, f.roles[0]}} {
		require.NoError(t, q.Create(&model.UserRole{TenantID: 1, UserID: grant[0], RoleID: grant[1]}).Error)
	}
	return f
}

func TestAdminNotificationBroadcastAudienceIntegrationComplete(t *testing.T) {
	f := newCompleteBroadcastFixture(t)
	for _, tc := range []struct {
		audience channel.Audience
		count    int64
	}{
		{channel.Audience{Type: "all"}, 4},
		{channel.Audience{Type: "roles", IDs: f.roles}, 2},
		{channel.Audience{Type: "users", IDs: []int64{f.alice.ID, f.bob.ID, f.alice.ID}}, 2},
	} {
		w := f.request(t, http.MethodPost, "/broadcasts/preview", "", map[string]any{"audience": tc.audience})
		require.Equal(t, 200, w.Code, w.Body.String())
		var result struct{ Recipients int64 }
		notificationDataF3(t, w, &result)
		require.Equal(t, tc.count, result.Recipients)
	}
	tenantAdmin := channel.Actor{TenantID: 1, UserID: f.user.ID}
	for _, a := range []channel.Audience{{Type: "users", IDs: []int64{f.foreign.ID}}, {Type: "roles", IDs: []int64{f.foreignRole}}} {
		_, err := f.manager.PreviewBroadcast(f.ctx, tenantAdmin, a)
		require.Equal(t, 404, errs.From(err).HTTPStatus)
	}
	for _, a := range []channel.Audience{{Type: "all", IDs: []int64{1}}, {Type: "unknown"}, {Type: "users"}, {Type: "roles", IDs: []int64{-1}}} {
		_, err := f.manager.PreviewBroadcast(f.ctx, f.actor, a)
		require.Equal(t, 400, errs.From(err).HTTPStatus)
	}
	// A super user in a tenant other than the configured platform stays tenant scoped.
	f.manager.platformTenantID = 2
	_, err := f.manager.PreviewBroadcast(f.ctx, f.actor, channel.Audience{Type: "users", IDs: []int64{f.foreign.ID}})
	require.Equal(t, 404, errs.From(err).HTTPStatus)
	_, err = f.manager.PreviewBroadcast(f.ctx, channel.Actor{TenantID: 2, UserID: f.foreign.ID, Super: true}, channel.Audience{Type: "users", IDs: []int64{f.alice.ID}})
	require.NoError(t, err)
	t.Log("`3 #8/#11: all=4 active users, overlapping roles=2, duplicated user IDs=2; foreign user/role404 for tenant admin; configured platform2 alone can cross tenants")
}

func TestAdminNotificationBroadcastDeliveryIntegrationComplete(t *testing.T) {
	f := newCompleteBroadcastFixture(t)
	addr, messages := completeSMTP(t)
	f.provider(t, "smtp", "broadcast", 0, true, channel.Config{"smtp_addr": addr, "from_address": f.email})
	in := channel.BroadcastInput{Title: "系统维护通知", Body: strings.Repeat("维护内容", 40), Channels: []string{"inbox", "email"}, Audience: channel.Audience{Type: "roles", IDs: f.roles[:1]}}
	var result channel.BroadcastResult
	for i := 0; i < 2; i++ {
		w := f.request(t, http.MethodPost, "/broadcasts", "announcement", in)
		require.Equal(t, 201, w.Code, w.Body.String())
		notificationDataF3(t, w, &result)
		require.Positive(t, result.BroadcastID)
		require.EqualValues(t, 1, result.Recipients)
	}
	changed := in
	changed.Body = "changed"
	require.Equal(t, 409, f.request(t, http.MethodPost, "/broadcasts", "announcement", changed).Code)
	f.assertInbox(t, f.alice, in.Title, in.Body, 1)
	for _, user := range []model.User{f.user, f.bob, f.foreign} {
		f.assertInbox(t, user, "", "", 0)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var deliveries []channel.Delivery
	require.NoError(t, q.Table("notify_delivery").Where("template_code='notification.broadcast'").Find(&deliveries).Error)
	require.Len(t, deliveries, 1)
	require.Equal(t, "pending", deliveries[0].Status)
	require.NotNil(t, deliveries[0].RecipientMasked)
	d, err := f.manager.delivery.Deliver(f.ctx, 1, deliveries[0].ID)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	msg := completeReceive(t, messages)
	require.Equal(t, *f.alice.Email, msg.Recipient)
	require.Contains(t, msg.Body, in.Body)
	select {
	case <-messages:
		t.Fatal("non-target or replay received an extra email")
	default:
	}
	var count int64
	require.NoError(t, q.Table("notification").Where("target=?", "user:"+fmtNotificationID(f.disabled.ID)).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, q.Table("notify_outbox").Where("template_code='notification.broadcast' AND status='sent'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	var after string
	require.NoError(t, q.Table("audit_log").Select("after::text").Where("action='system.notify.broadcast'").Take(&after).Error)
	var audit map[string]any
	require.NoError(t, json.Unmarshal([]byte(after), &audit))
	require.Equal(t, in.Title, audit["title"])
	require.EqualValues(t, 1, audit["recipients"])
	require.Equal(t, "roles", audit["audience"].(map[string]any)["type"])
	require.ElementsMatch(t, []any{"email", "inbox"}, audit["channels"])
	require.NoError(t, q.Table("audit_log").Where("action='system.notify.broadcast'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	f.assertBroadcastHistory(t, result)
	f.assertPrivacy(t, f.token, *f.alice.Email, *f.bob.Email, *f.foreign.Email)
	t.Log("`3 #9/#10/#12: target inbox contains exact title/body and120-character summary; four non-targets have0; actual SMTP RCPT/body correct; one outbox+delivery sent; replay no duplicate; body conflict409; one complete audit; history scoped")
}

func (f *completeBroadcastFixture) assertInbox(t *testing.T, user model.User, title, body string, count int) {
	t.Helper()
	n := newCredentialNotifications(&credentialRefreshRuntime{database: f.database, now: f.manager.now})
	rows, err := n.Inbox(f.ctx, user.TenantID, user.ID, 1, 20, notifier.InboxFilter{Template: "notification.broadcast"})
	require.NoError(t, err)
	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	var page struct {
		List  []credentialNotification
		Total int
	}
	require.NoError(t, json.Unmarshal(raw, &page))
	require.Equal(t, count, page.Total)
	require.Len(t, page.List, count)
	if count == 0 {
		return
	}
	require.Equal(t, title, page.List[0].Title)
	require.Equal(t, string([]rune(body)[:120]), page.List[0].Summary)
	var payload map[string]string
	require.NoError(t, json.Unmarshal(page.List[0].Payload, &payload))
	require.Equal(t, body, payload["body"])
	require.Equal(t, title, payload["title"])
}

func (f *completeBroadcastFixture) assertBroadcastHistory(t *testing.T, result channel.BroadcastResult) {
	t.Helper()
	w := f.request(t, http.MethodGet, "/broadcasts", "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var page struct {
		List  []channel.BroadcastView
		Total int
	}
	notificationDataF3(t, w, &page)
	require.Equal(t, 1, page.Total)
	require.Len(t, page.List, 1)
	require.Equal(t, result, page.List[0].BroadcastResult)
	other, err := f.manager.ListBroadcasts(f.ctx, channel.Actor{TenantID: 2, UserID: f.foreign.ID}, 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 0, other.(channel.Page).Total)
}

package bootstrap

import (
	"net/http"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestAdminNotificationBroadcastPermissionIntegrationComplete(t *testing.T) {
	f := newS3aFixture(t)
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO sys_role(tenant_id,code,name,data_scope,status) VALUES(1,'s3a-notify','notify','self','active')`))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO sys_user_role(tenant_id,user_id,role_id) SELECT 1,?,id FROM sys_role WHERE code='s3a-notify'`, f.user.ID))
	s3aGrant(t, f, permission.SystemNotifyManage)
	f.bearer = f.login(t).AccessToken
	for _, route := range [][2]string{{http.MethodGet, ""}, {http.MethodPost, ""}, {http.MethodPost, "/preview"}} {
		w := s3aRequest(t, f, route[0], "/notifications/broadcasts"+route[1], "", nil)
		require.Equal(t, 403, w.Code, w.Body.String())
	}
	s3aGrant(t, f, permission.SystemNotifyBroadcast)
	w := s3aRequest(t, f, http.MethodPost, "/notifications/broadcasts/preview", "", map[string]any{"audience": channel.Audience{Type: "all"}})
	require.Equal(t, 200, w.Code, w.Body.String())
	w = s3aRequest(t, f, http.MethodPost, "/notifications/broadcasts/preview", "", map[string]any{"audience": channel.Audience{Type: "users", IDs: []int64{999999}}})
	require.Equal(t, 404, w.Code, w.Body.String())
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Raw(`SELECT count(*) FROM sys_role_permission rp JOIN sys_role r ON r.tenant_id=rp.tenant_id AND r.id=rp.role_id
 JOIN sys_permission p ON p.tenant_id=rp.tenant_id AND p.id=rp.permission_id WHERE r.tenant_id=1 AND r.code='super_admin' AND p.code=?`, permission.SystemNotifyBroadcast).Scan(&count).Error)
	require.EqualValues(t, 1, count, "seed grants the new independent permission to built-in super_admin")
	t.Log("`3 #11: manage alone gives403 on all three broadcast routes; explicit broadcast grant succeeds; unavailable audience404; built-in super_admin seeded grant=1")
}

func TestAdminNotificationBroadcastEmailOnlyAndRollbackIntegrationComplete(t *testing.T) {
	f := newCompleteBroadcastFixture(t)
	in := channel.BroadcastInput{Title: "邮件公告", Body: "仅邮件", Channels: []string{"email"}, Audience: channel.Audience{Type: "users", IDs: []int64{f.alice.ID}}}
	w := f.request(t, http.MethodPost, "/broadcasts", "email-only", in)
	require.Equal(t, 201, w.Code, w.Body.String())
	f.assertInbox(t, f.alice, "", "", 0)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("notify_outbox").Where("template_code='notification.broadcast'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_complete_broadcast() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'injected audit failure'; END$$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER reject_complete_broadcast BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION reject_complete_broadcast()`))
	in.Channels = []string{"inbox", "email"}
	w = f.request(t, http.MethodPost, "/broadcasts", "rollback", in)
	require.Equal(t, 500, w.Code, w.Body.String())
	f.assertInbox(t, f.alice, "", "", 0)
	require.NoError(t, q.Table("notification").Where("target='broadcast'").Count(&count).Error)
	require.EqualValues(t, 1, count, "failed audit rolls back new history")
	require.NoError(t, q.Table("notify_outbox").Count(&count).Error)
	require.EqualValues(t, 1, count, "failed audit rolls back newly queued email")
	require.NoError(t, q.Table("idempotency_key").Where("key='rollback'").Count(&count).Error)
	require.Zero(t, count)
	t.Log("email-only has no inbox message; audit failure atomically rolls back history, inbox, delivery/outbox and idempotency record")
}

func TestAdminNotificationBroadcastInputComplete(t *testing.T) {
	base := channel.BroadcastInput{Title: "通知", Body: "正文", Channels: []string{"inbox"}, Audience: channel.Audience{Type: "all"}}
	for _, modify := range []func(*channel.BroadcastInput){
		func(in *channel.BroadcastInput) { in.Title = " " },
		func(in *channel.BroadcastInput) { in.Title = "标题\r\n注入" },
		func(in *channel.BroadcastInput) { in.Body = " " },
		func(in *channel.BroadcastInput) { in.Channels = []string{"sms"} },
		func(in *channel.BroadcastInput) { in.Channels = []string{"email", "email"} },
		func(in *channel.BroadcastInput) { in.Audience.Type = "invalid" },
	} {
		in := base
		modify(&in)
		_, err := validateBroadcast(in)
		require.Error(t, err)
		require.Equal(t, 400, errs.From(err).HTTPStatus)
	}
}

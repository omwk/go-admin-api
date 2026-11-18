package bootstrap

import (
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func enqueueReadableDelivery(t *testing.T, f *notificationFixtureF3, kind, code string) channel.Delivery {
	t.Helper()
	d, err := f.manager.delivery.Enqueue(f.ctx, 1, channel.Request{Channel: kind, TemplateCode: code, UserID: f.user.ID, Variables: map[string]string{"Name": "Ada"}})
	require.NoError(t, err)
	return d
}

func readableDeliveryPage(t *testing.T, f *notificationFixtureF3, query string) (int64, []channel.DeliveryView) {
	t.Helper()
	w := f.request(t, http.MethodGet, "/deliveries"+query, "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), f.email)
	require.NotContains(t, w.Body.String(), f.phone)
	var page struct {
		List  []channel.DeliveryView
		Total int64
	}
	notificationDataF3(t, w, &page)
	return page.Total, page.List
}

func TestAdminNotificationReadabilityIntegrationMasksAndNoPlaintext(t *testing.T) {
	// BE-IDN-015 §3 #2/#3: masks exist before any worker runs.
	f := newNotificationFixtureF3(t)
	f.email = "a" + hex.EncodeToString(f.secret[:8]) + "@163.com"
	f.phone = "138" + strings.Map(func(r rune) rune { return '0' + r%10 }, hex.EncodeToString(f.secret[:2])) + "1234"
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET email=?,phone=? WHERE id=?", f.email, f.phone, f.user.ID))
	email := enqueueReadableDelivery(t, f, "email", "notification.default")
	sms := enqueueReadableDelivery(t, f, "sms", "notification.default")
	require.Equal(t, "a***@163.com", *email.RecipientMasked)
	require.Equal(t, "138****1234", *sms.RecipientMasked)
	total, rows := readableDeliveryPage(t, f, "")
	require.EqualValues(t, 2, total)
	require.Equal(t, "138****1234", *rows[0].RecipientMasked)
	require.Equal(t, "a***@163.com", *rows[1].RecipientMasked)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var columns []struct{ ColumnName, DataType string }
	const columnSQL = "SELECT column_name,data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='notify_delivery' ORDER BY ordinal_position"
	require.NoError(t, q.Raw(columnSQL).Scan(&columns).Error)
	var leaks int64
	const leakSQL = "SELECT count(*) FROM notify_delivery d WHERE strpos(row_to_json(d)::text,?)>0 OR strpos(row_to_json(d)::text,?)>0"
	require.NoError(t, q.Raw(leakSQL, f.email, f.phone).Scan(&leaks).Error)
	require.Zero(t, leaks)
	t.Logf("§3 #3 SQL: %s -> %v", columnSQL, columns)
	t.Log("§3 #3 SQL: SELECT count(*) FROM notify_delivery d WHERE strpos(row_to_json(d)::text,$1)>0 OR strpos(row_to_json(d)::text,$2)>0 -> plaintext_rows=0 (bound generated email/phone)")
	t.Log("§3 #2 queued masks: a***@163.com, 138****1234; attempts=0")
	// Delivery resolves the current address and updates only the masked metadata.
	oldEmail := f.email
	f.email = "z" + hex.EncodeToString(f.secret[:8]) + "@example.invalid"
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET email=? WHERE id=?", f.email, f.user.ID))
	addr, calls := localSMTPF3(t)
	f.provider(t, "smtp", "masked-send", 0, true, channel.Config{"smtp_addr": addr, "from_address": f.email})
	sent, err := f.manager.delivery.Deliver(f.ctx, 1, email.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", sent.Status, sent.LastError)
	require.EqualValues(t, 1, calls.Load())
	_, rows = readableDeliveryPage(t, f, "?channel=email")
	require.Equal(t, "z***@example.invalid", *rows[0].RecipientMasked)
	f.assertPrivacy(t, f.token, oldEmail)
}

func TestAdminNotificationReadabilityIntegrationTemplateNamesAndFallback(t *testing.T) {
	// BE-IDN-015 §3 #1: one title per delivery; tenant override wins.
	f := newNotificationFixtureF3(t)
	enqueueReadableDelivery(t, f, "email", "notification.default")
	total, rows := readableDeliveryPage(t, f, "")
	require.EqualValues(t, 1, total)
	require.Equal(t, "系统通知", rows[0].TemplateName)
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO notify_template(tenant_id,code,locale,subject,body,format)
 VALUES(1,'notification.default','en','English','body','text'),
 (1,'notification.default','zh-CN','租户通知','内容','text')`))
	total, rows = readableDeliveryPage(t, f, "")
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	require.Equal(t, "租户通知", rows[0].TemplateName)
	require.NoError(t, f.database.Exec(f.ctx, "DELETE FROM notify_template WHERE code='notification.default'"))
	_, rows = readableDeliveryPage(t, f, "")
	require.Equal(t, "notification.default", rows[0].TemplateName)
	t.Log("§3 #1: 系统通知 -> 租户通知 (multiple locales, total=1) -> notification.default after deletion")
}

func TestAdminNotificationReadabilityIntegrationErrorsAndFilters(t *testing.T) {
	// BE-IDN-015 §3 #4/#5/#10: raw codes, translations and exact filters.
	f := newNotificationFixtureF3(t)
	for _, row := range []struct{ kind, code, status, failure string }{
		{"email", "notification.default", "failed", "message_unavailable"},
		{"email", "notification.test", "failed", "future_error"},
		{"sms", "notification.default", "pending", ""},
		{"sms", "notification.default", "failed", "recipient_unavailable"},
		{"webhook", "notification.test", "sent", ""},
	} {
		d := enqueueReadableDelivery(t, f, row.kind, row.code)
		require.NoError(t, f.database.Exec(f.ctx, "UPDATE notify_delivery SET status=?,last_error=? WHERE id=?", row.status, row.failure, d.ID))
	}
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO notify_delivery(tenant_id,channel,template_code,status)
 VALUES(2,'email','notification.default','failed')`))
	for _, tc := range []struct {
		query string
		total int
	}{
		{"", 5}, {"?status=failed", 3}, {"?channel=email", 2}, {"?template_code=notification.default", 3},
		{"?status=failed&channel=email", 2}, {"?status=failed&channel=email&template_code=notification.default", 1},
		{"?status=sent&channel=sms", 0}, {"?template_code=%27%20OR%201%3D1--", 0},
		{"?status=failed&page=2&page_size=1", 3},
	} {
		t.Run(tc.query, func(t *testing.T) {
			total, rows := readableDeliveryPage(t, f, tc.query)
			require.EqualValues(t, tc.total, total)
			if !strings.Contains(tc.query, "page=2") {
				require.Len(t, rows, tc.total)
			} else {
				require.Len(t, rows, 1)
			}
		})
	}
	_, rows := readableDeliveryPage(t, f, "?status=failed&channel=email")
	require.Equal(t, "future_error", rows[0].LastError)
	require.Equal(t, "future_error", rows[0].LastErrorMessage)
	require.Equal(t, "message_unavailable", rows[1].LastError)
	require.Equal(t, "消息内容已过期或模板已删除", rows[1].LastErrorMessage)
	for _, query := range []string{"?status=invalid", "?channel=invalid", "?page=0"} {
		require.Equal(t, 400, f.request(t, http.MethodGet, "/deliveries"+query, "", nil).Code)
	}
	t.Log("§3 #4/#5: message_unavailable -> 消息内容已过期或模板已删除; future_error -> future_error")
	t.Log("§3 #10: status=3, channel=2, template_code=3, combined=1, empty=0; page total=3; foreign tenant excluded")
}

func TestAdminNotificationReadabilityMigrationIntegrationThirtyThree(t *testing.T) {
	// BE-IDN-015 §3 #11: adding a nullable column preserves old sent rows.
	database, ctx := isolatedIdentityDB(t)
	migrationAt(ctx, t, database, 32)
	require.NoError(t, database.Exec(ctx, `INSERT INTO notify_delivery(tenant_id,channel,template_code,status,attempts,sent_at)
 VALUES(1,'email','notification.default','sent',1,now())`))
	state, err := database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, 33, state.Version)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var row struct {
		RecipientMasked *string
		Status          string
		Attempts        int
	}
	require.NoError(t, q.Table("notify_delivery").Select("recipient_masked,status,attempts").Take(&row).Error)
	require.Nil(t, row.RecipientMasked)
	require.Equal(t, "sent", row.Status)
	require.Equal(t, 1, row.Attempts)
	state, err = database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 32, state.Version)
	var columns int64
	require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='notify_delivery' AND column_name='recipient_masked'").Scan(&columns).Error)
	require.Zero(t, columns)
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, 33, state.Version)
	require.False(t, state.Dirty)
	t.Log("§3 #11: database.Migrate(up/down/up): 32 -> 33 -> 32 -> 33; old recipient_masked IS NULL; sent/attempts unchanged")
}

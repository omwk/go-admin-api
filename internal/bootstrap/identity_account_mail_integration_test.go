package bootstrap

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func TestIdentityAccountMailIntegrationVariablesAndPrivacy(t *testing.T) {
	// BE-IDN-016: variables come from tenant/user/token/settings, including optional contact.
	f := newAccountFixture(t)
	capture := accountMailProvider(t, f)
	f.accounts.Notify.(*accountNotifications).appName = "Acme Console"
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE tenant SET name='Acme' WHERE id=1"))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET nickname='张三' WHERE id=?", f.user.ID))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO sys_setting(tenant_id,group_key,item_key,value) VALUES(1,'notification','support_email','"support@example.invalid"')`))
	invite := f.invite(t, 1)
	require.Len(t, capture.messages, 1)
	variables := capture.messages[0].Variables
	require.Equal(t, "Acme Console", variables["AppName"])
	require.Equal(t, "张三", variables["UserName"])
	require.Equal(t, "Acme", variables["TenantName"])
	require.Equal(t, "168", variables["ExpiresInHours"])
	require.Equal(t, "support@example.invalid", variables["SupportEmail"])
	require.Contains(t, capture.messages[0].Body, "168 小时内有效")
	require.Contains(t, capture.messages[0].Body, "如遇问题请联系：support@example.invalid")
	assertAccountEncryptedLink(t, f, accountOutbox(t, f, "identity.invite"), invite.Link)
	reset, err := f.accounts.ForgotPassword(f.ctx, 1, f.user.Username, identity.RequestMeta{})
	require.NoError(t, err)
	out := accountOutbox(t, f, "identity.reset")
	delivery, err := f.notifications.delivery.Deliver(f.ctx, 1, out.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", delivery.Status)
	require.Len(t, capture.messages, 2)
	message := capture.messages[1]
	require.Equal(t, "30", message.Variables["ExpiresInHours"])
	require.Contains(t, message.Body, "张三 你好：")
	require.Contains(t, message.Body, "30 分钟内有效")
	assertAccountEncryptedLink(t, f, out, reset.Link)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var persisted []struct{ Data string }
	require.NoError(t, q.Raw(`SELECT row_to_json(d)::text AS data FROM notify_delivery d
 UNION ALL SELECT row_to_json(a)::text FROM audit_log a`).Scan(&persisted).Error)
	for _, row := range persisted {
		require.NotContains(t, row.Data, inviteRaw(t, invite.Link))
		require.NotContains(t, row.Data, inviteRaw(t, reset.Link))
	}
	t.Log("nickname, brand, tenant name, UTC expiry and contact resolved; invite=168 hours/reset=30 minutes; audit/delivery contain no raw credential")
}

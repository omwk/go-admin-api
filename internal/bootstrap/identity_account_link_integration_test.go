package bootstrap

import (
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestIdentityAccountNotificationIntegrationStableRetry(t *testing.T) {
	// F1: the approved encrypted outbox keeps copied and retried links identical.
	f := newAccountFixture(t)
	capture := accountMailProvider(t, f)
	reset, err := f.accounts.ForgotPassword(f.ctx, 1, f.user.Username, identity.RequestMeta{})
	require.NoError(t, err)
	out := accountOutbox(t, f, "identity.reset")
	var request channel.Request
	require.NoError(t, channel.OpenJSON(f.notifications.sealer, fmt.Sprintf("notification-outbox:v1:1:%d", out.ID), out.Payload, &request))
	require.True(t, reset.Link == request.Variables["Link"])
	raw := inviteRaw(t, reset.Link)
	require.NotContains(t, string(out.Payload), raw)
	capture.fail = true
	d, err := f.notifications.delivery.Deliver(f.ctx, 1, out.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", d.Status)
	require.Len(t, capture.messages, 1)
	require.True(t, reset.Link == capture.messages[0].Variables["Link"])
	capture.fail = false
	_, err = f.notifications.Retry(f.ctx, channel.Actor{TenantID: 1, UserID: f.user.ID, Super: true}, out.ID)
	require.NoError(t, err)
	d, err = f.notifications.delivery.Deliver(f.ctx, 1, out.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	require.Len(t, capture.messages, 2)
	require.True(t, reset.Link == capture.messages[1].Variables["Link"])
	row, found, err := f.accounts.Store.TokenByHash(f.ctx, 1, "reset", refresh.Hash(raw))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, refresh.Hash(raw), row.TokenHash)
	require.NoError(t, f.accounts.ResetPassword(f.ctx, 1, raw, accountPassword(t), identity.RequestMeta{}))
}

func TestIdentityAccountRecoveryIntegrationAcceptedContract(t *testing.T) {
	f := newAccountFixture(t)
	for _, copyLinks := range []bool{true, false} {
		f.accounts.CopyResetLinks = copyLinks
		for _, account := range []string{f.user.Username, "missing-member"} {
			response := f.request(t, "POST", "/auth/password/forgot", "", map[string]any{"account": account})
			require.Equal(t, 202, response.Code)
			require.Contains(t, response.Body.String(), `"accepted":true`)
			if copyLinks {
				require.Contains(t, response.Body.String(), `"dev_reset_url":`)
			} else {
				require.NotContains(t, response.Body.String(), "dev_reset_url")
				require.NotContains(t, response.Body.String(), "#token=")
			}
		}
	}
}

func TestIdentityAccountAdminIntegrationResetLinkAndAudit(t *testing.T) {
	f := newAccountFixture(t)
	f.accounts.CopyResetLinks = false
	invite := f.invite(t, 1)
	password := accountPassword(t)
	member, err := f.accounts.AcceptInvite(f.ctx, 1, identity.AcceptInviteInput{Token: inviteRaw(t, invite.Link), Username: "reset-link-member", Password: password}, identity.RequestMeta{})
	require.NoError(t, err)
	login, err := f.service.Login(f.ctx, 1, member.Username, password, identity.RequestMeta{})
	require.NoError(t, err)
	path := fmt.Sprintf("/admin/users/%d/reset-link", member.ID)
	require.Equal(t, 401, f.request(t, "POST", path, "", nil).Code)
	require.Equal(t, 403, f.request(t, "POST", path, login.AccessToken, nil).Code)
	response := f.request(t, "POST", path, f.token, nil)
	require.Equal(t, 201, response.Code, response.Body.String())
	var result struct {
		Link string `json:"link"`
	}
	notificationDataF3(t, response, &result)
	raw := inviteRaw(t, result.Link)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var events []struct {
		ActorID    int64
		ResourceID string
		Data       string
	}
	require.NoError(t, q.Table("audit_log").Select("actor_id,resource_id,COALESCE(after::text,'') AS data").Where("action=?", "identity.password.reset_link").Find(&events).Error)
	require.Len(t, events, 1)
	require.Equal(t, f.user.ID, events[0].ActorID)
	require.Equal(t, fmt.Sprint(member.ID), events[0].ResourceID)
	require.NotContains(t, events[0].Data, raw)
	require.NoError(t, f.accounts.ResetPassword(f.ctx, 1, raw, accountPassword(t), identity.RequestMeta{}))
	require.ErrorContains(t, f.accounts.ResetPassword(f.ctx, 1, raw, accountPassword(t), identity.RequestMeta{}), "邀请或链接无效")
}

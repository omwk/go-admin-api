package bootstrap

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestIdentityAccountNotificationIntegrationInviteChannelRecovery(t *testing.T) {
	f := newAccountFixture(t)
	invite := f.invite(t, 1)
	out := accountOutbox(t, f, "identity.invite")
	capture := accountMailProvider(t, f)
	_, err := f.notifications.Retry(f.ctx, channel.Actor{TenantID: 1, UserID: f.user.ID, Super: true}, out.ID)
	require.NoError(t, err)
	d, err := f.notifications.delivery.Deliver(f.ctx, 1, out.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	require.Len(t, capture.messages, 1)
	require.True(t, invite.Link == capture.messages[0].Variables["Link"])
	_, err = f.accounts.AcceptInvite(f.ctx, 1, identity.AcceptInviteInput{Token: inviteRaw(t, invite.Link), Username: "restored-member", Password: accountPassword(t)}, identity.RequestMeta{})
	require.NoError(t, err)
}

func TestIdentityAccountNotificationIntegrationKeylessPrivacy(t *testing.T) {
	f := newAccountFixture(t)
	sealer := f.notifications.sealer
	f.notifications.sealer, f.notifications.delivery.Sealer = nil, nil
	invite := f.invite(t, 1)
	out := accountOutbox(t, f, "identity.invite")
	require.NotContains(t, string(out.Payload), inviteRaw(t, invite.Link))
	require.NotContains(t, string(out.Payload), "#token=")
	require.Contains(t, string(out.Payload), "TokenID")
	f.notifications.sealer, f.notifications.delivery.Sealer = sealer, sealer
	capture := accountMailProvider(t, f)
	_, err := f.notifications.Retry(f.ctx, channel.Actor{TenantID: 1, UserID: f.user.ID, Super: true}, out.ID)
	require.NoError(t, err)
	d, err := f.notifications.delivery.Deliver(f.ctx, 1, out.ID)
	require.NoError(t, err)
	require.Equal(t, "message_unavailable", d.LastError)
	require.Empty(t, capture.messages)
	_, err = f.accounts.AcceptInvite(f.ctx, 1, identity.AcceptInviteInput{Token: inviteRaw(t, invite.Link), Username: "keyless-member", Password: accountPassword(t)}, identity.RequestMeta{})
	require.NoError(t, err)
}

func TestIdentityAccountRecoveryIntegrationProductionChannelAlert(t *testing.T) {
	f := newAccountFixture(t)
	f.accounts.CopyResetLinks = false
	f.accounts.Notify.(*accountNotifications).production = true
	var logs bytes.Buffer
	f.ctx = logger.With(f.ctx, slog.New(slog.NewJSONHandler(&logs, nil)))
	response := f.request(t, "POST", "/auth/password/forgot", "", map[string]any{"account": f.user.Username})
	require.Equal(t, 202, response.Code)
	require.Contains(t, response.Body.String(), `"data":{"accepted":true}`)
	out := accountOutbox(t, f, "identity.reset")
	require.Equal(t, "channel_unavailable", out.Status)
	require.Contains(t, logs.String(), `"level":"WARN"`)
	require.Contains(t, logs.String(), `"request_id":"identity-integration-trace"`)
	require.NotContains(t, logs.String(), f.user.Username)
	require.NotContains(t, logs.String(), "#token=")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("notification").Where("tenant_id=1 AND template='notification.delivery_failed' AND channel='inbox'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(NewAccountMetrics(f.ctx, f.database)))
	metrics, err := registry.Gather()
	require.NoError(t, err)
	require.Equal(t, "password_reset_channel_unavailable", metrics[0].GetName())
	require.Equal(t, 1.0, metrics[0].Metric[0].Gauge.GetValue())
	capture := accountMailProvider(t, f)
	f.now = f.now.Add(6 * time.Minute)
	d, err := f.notifications.delivery.Deliver(f.ctx, 1, out.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	require.Len(t, capture.messages, 1)
	metrics, err = registry.Gather()
	require.NoError(t, err)
	require.Zero(t, metrics[0].Metric[0].Gauge.GetValue())
}

package bootstrap

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func TestAdminNotificationIntegrationCredentialUsesManagedProvider(t *testing.T) {
	f, n, alert := notificationFixture(t)
	addr, calls := localSMTPF3(t)
	email := f.randomValue(t) + "@example.invalid"
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET email=? WHERE id=?", email, f.actor.UserID))
	require.NoError(t, n.SaveSettings(f.ctx, 1, f.actor.UserID, notifier.Settings{EmailEnabled: true, SMTPAddr: addr, SMTPFrom: email}))
	m := newNotificationManager(&notificationStore{database: f.database}, n.runtime.sealer, n.now)
	actor := channel.Actor{TenantID: 1, UserID: f.actor.UserID}
	in := channel.ProviderInput{Channel: "email", Kind: "smtp", Name: "managed", Enabled: true, Config: channel.Config{"smtp_addr": addr, "from_address": email}}
	out, err := m.SaveProvider(f.ctx, actor, 0, in)
	require.NoError(t, err)
	provider := out.(channel.ProviderView)
	require.NoError(t, n.notify(f.ctx, alert))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var legacy int64
	require.NoError(t, q.Table("notification").Where("channel='email'").Count(&legacy).Error)
	require.Zero(t, legacy)
	d, err := m.delivery.Deliver(f.ctx, 1, 0)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	require.Equal(t, provider.ID, *d.ProviderID)
	require.Nil(t, d.NextRetryAt)
	require.EqualValues(t, 1, calls.Load())
	in.Enabled = false
	_, err = m.SaveProvider(f.ctx, actor, provider.ID, in)
	require.NoError(t, err)
	alert.Severity = "warning"
	require.NoError(t, n.notify(f.ctx, alert))
	d, err = m.delivery.Deliver(f.ctx, 1, 0)
	require.NoError(t, err)
	require.Equal(t, "skipped", d.Status)
	require.Equal(t, "channel_unavailable", d.LastError)
	require.EqualValues(t, 1, calls.Load())
	require.NoError(t, q.Table("notification").Where("channel='email'").Count(&legacy).Error)
	require.Zero(t, legacy)
	f.assertSafe(t)
}

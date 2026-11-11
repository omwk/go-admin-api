package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func TestAdminNotificationIntegrationSMSHashAndRevokedRecipient(t *testing.T) {
	f := newNotificationFixtureF3(t)
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.NoError(t, r.ParseForm())
		require.Equal(t, f.phone, r.Form.Get("PhoneNumbers"))
		_, err := w.Write([]byte(`{"Code":"OK"}`))
		require.NoError(t, err)
	}))
	defer server.Close()
	f.provider(t, "aliyun_sms", "SMS", 0, true, channel.Config{"endpoint": server.URL, "access_key_id": f.token, "access_key_secret": f.token, "region": "mock", "sign_name": "mock", "template_code": "mock"})
	d := f.send(t, "sms")
	require.Equal(t, "sent", d.Status)
	require.Len(t, d.RecipientHash, 64)
	require.Empty(t, d.RecipientDomain)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET status='disabled' WHERE id=?", f.user.ID))
	d = f.send(t, "sms")
	require.Equal(t, "failed", d.Status)
	require.Equal(t, "recipient_unavailable", d.LastError)
	require.EqualValues(t, 1, calls.Load())
	f.assertPrivacy(t, f.token)
}

func TestAdminNotificationIntegrationGlobalUniquenessAndCipherSubstitution(t *testing.T) {
	f := newNotificationFixtureF3(t)
	server, calls := mockAcceptedF3(t)
	in := channel.ProviderInput{Channel: "webhook", Kind: "http", Name: "global", Global: true, Enabled: true, Config: channel.Config{"endpoint": server.URL, "secret": f.token}}
	out, err := f.manager.SaveProvider(f.ctx, f.actor, 0, in)
	require.NoError(t, err)
	p := out.(channel.ProviderView)
	require.Nil(t, p.TenantID)
	_, err = f.manager.SaveProvider(f.ctx, f.actor, 0, in)
	require.Error(t, err)
	_, err = f.manager.SaveProvider(f.ctx, channel.Actor{TenantID: 1, UserID: f.user.ID}, p.ID, in)
	require.Error(t, err)
	local := f.provider(t, "http", "local", -1, true, in.Config)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE channel_provider SET config=(SELECT config FROM channel_provider WHERE id=?) WHERE id=?", p.ID, local.ID))
	d := f.send(t, "webhook")
	require.Equal(t, "sent", d.Status)
	require.Equal(t, 2, d.Attempts)
	require.Equal(t, p.ID, *d.ProviderID)
	require.EqualValues(t, 1, calls.Load())
	f.assertPrivacy(t, f.token)
}

func TestAdminNotificationIntegrationTestLimitAcrossManagersAndTenants(t *testing.T) {
	f := newNotificationFixtureF3(t)
	var wg sync.WaitGroup
	accepted := &atomic.Int32{}
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := newNotificationManager(f.manager.store, f.manager.sealer, f.manager.now)
			err := m.store.Transaction(f.ctx, func(ctx context.Context) error { return m.testRate(ctx, 1) })
			if err == nil {
				accepted.Add(1)
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			require.Contains(t, err.Error(), "三次")
		}
	}
	require.EqualValues(t, 3, accepted.Load())
	require.NoError(t, f.manager.store.Transaction(f.ctx, func(ctx context.Context) error { return f.manager.testRate(ctx, 2) }))
	f.now = f.now.Add(time.Minute)
	require.NoError(t, f.manager.store.Transaction(f.ctx, func(ctx context.Context) error { return f.manager.testRate(ctx, 1) }))
}

func TestAdminNotificationIntegrationNoEncryptionConfigurationAndLeaseRecovery(t *testing.T) {
	f := newNotificationFixtureF3(t)
	keyless := channel.NewService(f.manager.store, nil, f.manager.now)
	d, err := keyless.Send(f.ctx, 1, channel.Request{Channel: "email", TemplateCode: "notification.default", UserID: f.user.ID, Variables: map[string]string{"Name": "Ada"}})
	require.NoError(t, err)
	require.Equal(t, "skipped", d.Status)
	server, calls := mockAcceptedF3(t)
	f.provider(t, "http", "local", 0, true, channel.Config{"endpoint": server.URL, "secret": f.token})
	d, err = f.manager.delivery.Enqueue(f.ctx, 1, channel.Request{Channel: "webhook", TemplateCode: "notification.test", UserID: f.user.ID})
	require.NoError(t, err)
	_, _, found, err := f.manager.store.Claim(f.ctx, 1, d.ID, f.now)
	require.NoError(t, err)
	require.True(t, found)
	_, _, found, err = f.manager.store.Claim(f.ctx, 1, d.ID, f.now)
	require.NoError(t, err)
	require.False(t, found)
	f.now = f.now.Add(4 * time.Minute)
	d, err = f.manager.delivery.Deliver(f.ctx, 1, d.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	require.EqualValues(t, 1, calls.Load())
	f.assertPrivacy(t, f.token)
}

package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

// F3 §5.1/3: absence and disablement remain durable, non-failing business results.
func TestAdminNotificationIntegrationUnavailableAndDisabled(t *testing.T) {
	f := newNotificationFixtureF3(t)
	d := f.send(t, "email")
	require.Equal(t, "skipped", d.Status)
	require.Equal(t, "channel_unavailable", d.LastError)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var state string
	require.NoError(t, q.Table("notify_outbox").Select("status").Where("id=?", d.ID).Take(&state).Error)
	require.Equal(t, "channel_unavailable", state)
	addr, calls := localSMTPF3(t)
	f.provider(t, "smtp", "disabled", 0, false, channel.Config{"smtp_addr": addr, "from_address": f.email})
	d = f.send(t, "email")
	require.Equal(t, "skipped", d.Status)
	require.Zero(t, calls.Load())
	f.assertPrivacy(t)
}

// F3 §5.4/5/8: actual local transports, attempt counts and administrator escalation.
func TestAdminNotificationIntegrationFallbackFailureAndPrivacy(t *testing.T) {
	f := newNotificationFixtureF3(t)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer failing.Close()
	secret := f.token
	primary := f.provider(t, "sendgrid", "primary", 0, true, channel.Config{"endpoint": failing.URL + "?key=" + secret, "api_key": secret, "from_address": f.email})
	addr, sent := localSMTPF3(t)
	secondary := f.provider(t, "smtp", "secondary", 1, true, channel.Config{"smtp_addr": addr, "from_address": f.email})
	d := f.send(t, "email")
	require.Equal(t, "sent", d.Status)
	require.Equal(t, 2, d.Attempts)
	require.Equal(t, secondary.ID, *d.ProviderID)
	require.EqualValues(t, 1, sent.Load())
	require.Len(t, d.RecipientHash, 64)
	require.Equal(t, "example.invalid", d.RecipientDomain)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE channel_provider SET enabled=false WHERE id=?", secondary.ID))
	d = f.send(t, "email")
	require.Equal(t, "failed", d.Status)
	require.Equal(t, 1, d.Attempts)
	require.Equal(t, primary.ID, *d.ProviderID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var alerts int64
	require.NoError(t, q.Table("notification").Where("tenant_id=1 AND channel='inbox' AND template='notification.delivery_failed'").Count(&alerts).Error)
	require.EqualValues(t, 1, alerts)
	f.assertPrivacy(t, secret)
}

// F3 §5.6: invalid tenant overrides fail before calling any provider.
func TestAdminNotificationIntegrationTemplatesAndTenantIsolation(t *testing.T) {
	f := newNotificationFixtureF3(t)
	server, calls := mockAcceptedF3(t)
	p := f.provider(t, "http", "webhook", 0, true, channel.Config{"endpoint": server.URL, "secret": f.token})
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("INSERT INTO notify_template(tenant_id,code,locale,subject,body,format) VALUES(1,'notification.default','zh-CN','override','{{.Unknown}}','text')").Error)
	d := f.send(t, "webhook")
	require.Equal(t, "failed", d.Status)
	require.Equal(t, "template_render_failed", d.LastError)
	require.Zero(t, calls.Load())
	require.NoError(t, q.Exec("UPDATE notify_template SET body='{{.Name}}' WHERE tenant_id=1").Error)
	d = f.send(t, "webhook")
	require.Equal(t, "sent", d.Status)
	require.EqualValues(t, 1, calls.Load())
	_, err := f.manager.provider(f.ctx, channel.Actor{TenantID: 2, UserID: f.user.ID, Super: true}, p.ID, true)
	require.Error(t, err)
	var count int64
	other, err := f.manager.ListDeliveries(f.ctx, channel.Actor{TenantID: 2}, 1, 20)
	require.NoError(t, err)
	require.Zero(t, other.(channel.Page).Total)
	require.NoError(t, q.Table("notify_template").Where("tenant_id IS NULL AND code='notification.default'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	f.assertPrivacy(t, f.token)
}

func TestAdminNotificationIntegrationTransactionsRetryAndLease(t *testing.T) {
	f := newNotificationFixtureF3(t)
	err := f.database.Transaction(f.ctx, func(ctx context.Context) error {
		_, err := f.manager.delivery.Enqueue(ctx, 1, channel.Request{Channel: "webhook", TemplateCode: "notification.test", UserID: f.user.ID})
		require.NoError(t, err)
		return errors.New("rollback business transaction")
	})
	require.Error(t, err)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("notify_delivery").Count(&count).Error)
	require.Zero(t, count)
	d := f.send(t, "webhook")
	require.Equal(t, "skipped", d.Status)
	server, calls := mockAcceptedF3(t)
	f.provider(t, "http", "restored", 0, true, channel.Config{"endpoint": server.URL, "secret": f.token})
	f.now = f.now.Add(6 * time.Minute)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.manager.delivery.Deliver(f.ctx, 1, d.ID); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, calls.Load())
	d, err = f.manager.deliveryByID(f.ctx, 1, d.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	f.assertPrivacy(t, f.token)
}

func TestAdminNotificationMigrationIntegrationTwentyThreeTwentyTwoTwentyThree(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	state := migrationAt(ctx, t, database, 23)
	require.EqualValues(t, 23, state.Version)
	state, err := database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 22, state.Version)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var absent bool
	require.NoError(t, q.Raw("SELECT to_regclass('channel_provider') IS NULL AND to_regclass('notify_template') IS NULL AND to_regclass('notify_delivery') IS NULL AND to_regclass('notify_outbox') IS NULL").Scan(&absent).Error)
	require.True(t, absent)
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, latestBootstrapMigration(t), state.Version)
	require.False(t, state.Dirty)
	require.NoError(t, q.Raw("SELECT to_regclass('channel_provider') IS NULL OR to_regclass('notify_template') IS NULL OR to_regclass('notify_delivery') IS NULL OR to_regclass('notify_outbox') IS NULL").Scan(&absent).Error)
	require.False(t, absent)
}

package bootstrap

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func TestAdminNotificationRobotRoutingIntegration(t *testing.T) {
	// BE-IDN-019 §3 #6/#7: personal robot use is denied; announcements use one tenant robot delivery.
	f := newCompleteBroadcastFixture(t)
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, err := w.Write([]byte(`{"errcode":0}`))
		require.NoError(t, err)
	}))
	defer server.Close()
	in := channel.ProviderInput{Channel: "wecom", Kind: "wecom_bot", Name: "announcements", Enabled: true, Scope: "personal", Config: channel.Config{"endpoint": server.URL}}
	_, err := f.manager.SaveProvider(f.ctx, f.actor, 0, in)
	require.Error(t, err)
	in.Scope = "system"
	provider, err := f.manager.SaveProvider(f.ctx, f.actor, 0, in)
	require.NoError(t, err)
	require.Equal(t, "system", provider.(channel.ProviderView).Scope)
	require.Contains(t, provider.(channel.ProviderView).Description, "不发送个人通知")
	for _, kind := range []string{"wecom", "dingtalk", "sms"} {
		d, err := f.manager.delivery.Send(f.ctx, 1, channel.Request{Channel: kind, UserID: f.user.ID, Target: "user:" + fmtNotificationID(f.user.ID), TemplateCode: "notification.default", Variables: map[string]string{"Name": "A"}})
		require.NoError(t, err)
		require.Equal(t, "skipped", d.Status)
		require.Equal(t, "not_personal_channel", d.LastError)
		require.Zero(t, d.Attempts)
		stored, err := f.manager.deliveryByID(f.ctx, 1, d.ID)
		require.NoError(t, err)
		require.Equal(t, d.LastError, stored.LastError)
	}
	require.Zero(t, calls.Load())
	w := f.request(t, http.MethodPost, "/broadcasts", "robot-announcement", channel.BroadcastInput{Title: "维护", Body: "公告正文", Channels: []string{"wecom"}, Audience: channel.Audience{Type: "users", IDs: []int64{f.alice.ID, f.bob.ID}}})
	require.Equal(t, 201, w.Code, w.Body.String())
	d, err := f.manager.delivery.Deliver(f.ctx, 1, 0)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	require.EqualValues(t, 1, calls.Load())
	d, err = f.manager.delivery.Deliver(f.ctx, 1, 0)
	require.NoError(t, err)
	require.Zero(t, d.ID)
	t.Log("#6/#7: wecom/dingtalk/SMS personal targets persisted skipped/not_personal_channel with 0 attempts; wecom scope=system; announcement for 2 users made exactly 1 successful robot POST")
}

func TestIdentityNotificationRoutingMigrationIntegration(t *testing.T) {
	// BE-IDN-019 §3 #11: immutable 000035 supports an actual down/up cycle.
	database, ctx := isolatedIdentityDB(t)
	latest, err := database.Migrate(ctx, "version")
	require.NoError(t, err)
	state := latest
	for state.Version > 34 {
		state, err = database.Migrate(ctx, "down")
		require.NoError(t, err)
	}
	require.EqualValues(t, 34, state.Version)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var absent bool
	require.NoError(t, q.Raw("SELECT to_regclass('sys_user_channel') IS NULL").Scan(&absent).Error)
	require.True(t, absent)
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.Equal(t, latest.Version, state.Version)
	require.False(t, state.Dirty)
	var dataType string
	require.NoError(t, q.Raw("SELECT data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='sys_user_channel' AND column_name='endpoint_enc'").Scan(&dataType).Error)
	require.Equal(t, "bytea", dataType)
	t.Logf("#11: %d -> down 34 (sys_user_channel absent) -> up %d clean; endpoint_enc=bytea", latest.Version, state.Version)
}

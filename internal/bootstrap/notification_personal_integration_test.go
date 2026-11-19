package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func TestIdentityNotificationPersonalCRUDIntegration(t *testing.T) {
	// BE-IDN-019 §3 #1/#2/#4: real HTTP, sealed storage, masked responses and deletion.
	f := newNotificationFixtureF3(t)
	router := personalRouter(t, f)
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, map[string]string{"title": "通道测试", "body": "通知通道测试"}, body)
		w.WriteHeader(200)
	}))
	defer server.Close()
	localPersonalTransport(t, f.manager, server.URL)
	for _, kind := range []string{"webhook", "bark", "gotify"} {
		endpoint := "https://personal.example.invalid/" + personalRandom(t)
		input := map[string]any{"endpoint": endpoint, "user_id": f.user.ID + 1, "tenant_id": 2}
		for i := 0; i < 2; i++ {
			w := personalHTTP(t, f, router, f.token, http.MethodPut, "notification-channels/"+kind, kind, input)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.NotContains(t, w.Body.String(), endpoint)
		}
		row, err := f.manager.store.personalChannel(f.ctx, 1, f.user.ID, kind)
		require.NoError(t, err)
		require.Nil(t, row.VerifiedAt)
		require.NotContains(t, string(row.EndpointEnc), endpoint)
		plain, err := f.manager.store.openPersonalEndpoint(row)
		require.NoError(t, err)
		require.Equal(t, endpoint, plain)
		w := personalHTTP(t, f, router, f.token, http.MethodPost, "notification-channels/"+kind+"/test", "test-"+kind, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		var delivery channel.Delivery
		notificationDataF3(t, w, &delivery)
		require.Equal(t, "sent", delivery.Status)
		require.Nil(t, delivery.ProviderID)
		row, err = f.manager.store.personalChannel(f.ctx, 1, f.user.ID, kind)
		require.NoError(t, err)
		require.NotNil(t, row.VerifiedAt)
		w = personalHTTP(t, f, router, f.token, http.MethodGet, "notification-channels", "", nil)
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), "https://personal.example.invalid/…/****")
		require.NotContains(t, w.Body.String(), endpoint)
		w = personalHTTP(t, f, router, f.token, http.MethodDelete, "notification-channels/"+kind, "delete-"+kind, nil)
		require.Equal(t, 204, w.Code)
		require.Equal(t, 204, personalHTTP(t, f, router, f.token, http.MethodDelete, "notification-channels/"+kind, "delete-"+kind, nil).Code)
		_, err = f.manager.store.personalChannel(f.ctx, 1, f.user.ID, kind)
		require.Error(t, err)
		f.assertPrivacy(t, endpoint)
	}
	require.EqualValues(t, 3, calls.Load())
	t.Log("#1/#2/#4: webhook/bark/gotify CRUD=200/204; bytea decrypts with owner AAD; responses mask URL; local HTTP received 3 title/body POSTs; verification only after success")
}

func TestIdentityNotificationPersonalSSRFIntegration(t *testing.T) {
	// BE-IDN-019 §3 #3: reject unsafe inputs without echoing the URL.
	f := newNotificationFixtureF3(t)
	router := personalRouter(t, f)
	for _, endpoint := range []string{"https://127.0.0.1/", "https://10.0.0.1/", "https://[::1]/", "https://169.254.169.254/", "http://example.invalid/", "https://example.invalid/" + strings.Repeat("a", 2048)} {
		w := personalHTTP(t, f, router, f.token, http.MethodPut, "notification-channels/bark", "", map[string]any{"endpoint": endpoint})
		require.Equal(t, 400, w.Code)
		require.NotContains(t, w.Body.String(), endpoint)
	}
	for _, kind := range []string{"email", "sms", "wecom", "dingtalk"} {
		require.Equal(t, 400, personalHTTP(t, f, router, f.token, http.MethodPut, "notification-channels/"+kind, "", map[string]any{"endpoint": "https://127.0.0.1/"}).Code)
	}
	require.Equal(t, 401, personalHTTP(t, f, router, "", http.MethodGet, "notification-channels", "", nil).Code)
	t.Log("#3: private/loopback/metadata/IPv6/HTTP/oversized URLs=400; unsupported channels=400; unauthenticated=401; no URL echoed")
}

func TestIdentityNotificationPersonalPreferencesIntegration(t *testing.T) {
	// BE-IDN-019 §3 #8: capability reasons are scoped to the current user.
	f := newNotificationFixtureF3(t)
	router := personalRouter(t, f)
	n := newCredentialNotifications(&credentialRefreshRuntime{database: f.database, sealer: f.manager.sealer, now: f.manager.now})
	assertState := func(kind, reason string, available bool) {
		w := personalHTTP(t, f, router, f.token, http.MethodGet, "notification-preferences", "", nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		var out struct{ Preferences []notifier.PreferenceView }
		notificationDataF3(t, w, &out)
		found := false
		for _, p := range out.Preferences {
			require.NotContains(t, []string{"sms", "wecom", "dingtalk"}, p.Channel)
			if p.Channel == kind {
				found = true
				require.Equal(t, available, p.Available)
				require.Equal(t, reason, p.Reason)
			}
		}
		require.True(t, found)
	}
	assertState("bark", "not_configured", false)
	assertState("email", "", true)
	server, _ := mockAcceptedF3(t)
	savePersonalFixture(f.ctx, t, f.manager, f.actor, "bark", server.URL, false)
	assertState("bark", "not_verified", false)
	require.Equal(t, 200, personalHTTP(t, f, router, f.token, http.MethodPost, "notification-channels/bark/test", "", nil).Code)
	assertState("bark", "", true)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user_channel SET enabled=false WHERE tenant_id=1 AND user_id=?", f.user.ID))
	assertState("bark", "disabled", false)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET email=NULL WHERE id=?", f.user.ID))
	assertState("email", "missing_email", false)
	enabled := true
	_, err := n.SavePreferences(f.ctx, 1, f.user.ID, []notifier.Preference{{Template: "notification.default", Channel: "sms", Enabled: &enabled}})
	require.Error(t, err)
	t.Log("#8: not_configured -> not_verified -> available -> disabled; missing_email reported; SMS and robots absent and rejected")
}

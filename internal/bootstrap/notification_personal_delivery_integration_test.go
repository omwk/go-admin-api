package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

type personalHTTPMessage struct{ Path, Title, Body string }

func personalMessageServer(t *testing.T) (*httptest.Server, <-chan personalHTTPMessage) {
	t.Helper()
	messages := make(chan personalHTTPMessage, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Title, Body string }
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		messages <- personalHTTPMessage{Path: r.URL.Path, Title: body.Title, Body: body.Body}
		w.WriteHeader(200)
	}))
	t.Cleanup(server.Close)
	return server, messages
}

func TestIdentityNotificationPersonalRoutingIsolationIntegration(t *testing.T) {
	// BE-IDN-019 §3 #5/#10: real local HTTP asserts the two users' exact destination paths.
	f := newNotificationFixtureF3(t)
	router := personalRouter(t, f)
	server, messages := personalMessageServer(t)
	localPersonalTransport(t, f.manager, server.URL)
	bob := model.User{TenantID: 1, Username: "personal-bob", DepartmentID: f.user.DepartmentID, PasswordHash: f.user.PasswordHash, Status: "active"}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Create(&bob).Error)
	bobToken, err := f.service.Login(f.ctx, 1, bob.Username, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	endpoints := map[int64]string{}
	for _, actor := range []struct {
		id    int64
		token string
	}{{f.user.ID, f.token}, {bob.ID, bobToken.AccessToken}} {
		endpoint := "https://personal.example.invalid/" + personalRandom(t)
		endpoints[actor.id] = endpoint
		w := personalHTTP(t, f, router, actor.token, http.MethodPut, "notification-channels/bark", "", map[string]any{"endpoint": endpoint, "user_id": f.user.ID})
		require.Equal(t, 200, w.Code)
		require.Equal(t, 200, personalHTTP(t, f, router, actor.token, http.MethodPost, "notification-channels/bark/test", "", nil).Code)
		<-messages
	}
	for _, actor := range []struct {
		id   int64
		name string
	}{{f.user.ID, "A"}, {bob.ID, "B"}} {
		d, err := f.manager.delivery.Send(f.ctx, 1, channel.Request{Channel: "bark", UserID: actor.id, Target: "user:" + fmtNotificationID(actor.id), TemplateCode: "notification.default", Variables: map[string]string{"Name": actor.name}})
		require.NoError(t, err)
		require.Equal(t, "sent", d.Status)
		require.Equal(t, "bark", d.Channel)
		message := <-messages
		u, err := url.Parse(endpoints[actor.id])
		require.NoError(t, err)
		require.Equal(t, u.Path, message.Path)
		require.Contains(t, message.Body, actor.name)
	}
	require.NoError(t, q.Exec("UPDATE sys_user_channel SET endpoint_enc=(SELECT endpoint_enc FROM sys_user_channel WHERE user_id=? AND channel='bark') WHERE user_id=? AND channel='bark'", f.user.ID, bob.ID).Error)
	d, err := f.manager.delivery.Send(f.ctx, 1, channel.Request{Channel: "bark", UserID: bob.ID, TemplateCode: "notification.default", Variables: map[string]string{"Name": "B"}})
	require.NoError(t, err)
	require.Equal(t, "recipient_unavailable", d.LastError)
	_, err = f.manager.store.Recipient(f.ctx, 2, f.user.ID, "bark")
	require.Error(t, err)
	select {
	case <-messages:
		t.Fatal("foreign user or substituted ciphertext reached a destination")
	default:
	}
	for _, endpoint := range endpoints {
		f.assertPrivacy(t, endpoint)
	}
	t.Log("#5/#10: A's Bark notification arrived only at A's random path; B arrived only at B's; owner AAD substitution and foreign tenant produced zero HTTP requests")
}

func TestIdentityNotificationPersonalFallbackIntegration(t *testing.T) {
	// BE-IDN-019 §3 #9: both pending work and new requests recheck destination and preference.
	f := newNotificationFixtureF3(t)
	server, messages := personalMessageServer(t)
	savePersonalFixture(f.ctx, t, f.manager, f.actor, "bark", server.URL, true)
	addr, sent := localSMTPF3(t)
	f.provider(t, "smtp", "fallback", 0, true, channel.Config{"smtp_addr": addr, "from_address": f.email})
	in := channel.Request{Channel: "bark", UserID: f.user.ID, TemplateCode: "notification.default", Variables: map[string]string{"Name": "A"}}
	d, err := f.manager.delivery.Enqueue(f.ctx, 1, in)
	require.NoError(t, err)
	require.Equal(t, "bark", d.Channel)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user_channel SET enabled=false WHERE tenant_id=1 AND user_id=?", f.user.ID))
	d, err = f.manager.delivery.Deliver(f.ctx, 1, d.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	require.Equal(t, "email", d.Channel)
	stored, err := f.manager.deliveryByID(f.ctx, 1, d.ID)
	require.NoError(t, err)
	require.Equal(t, "email", stored.Channel)
	require.EqualValues(t, 1, sent.Load())
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user_channel SET enabled=true WHERE tenant_id=1 AND user_id=?", f.user.ID))
	n := newCredentialNotifications(&credentialRefreshRuntime{database: f.database, sealer: f.manager.sealer, now: f.manager.now})
	disabled := false
	_, err = n.SavePreferences(f.ctx, 1, f.user.ID, []notifier.Preference{{Template: in.TemplateCode, Channel: "bark", Enabled: &disabled}})
	require.NoError(t, err)
	d, err = f.manager.delivery.Send(f.ctx, 1, in)
	require.NoError(t, err)
	require.Equal(t, "email", d.Channel)
	require.Equal(t, "sent", d.Status)
	require.EqualValues(t, 2, sent.Load())
	select {
	case <-messages:
		t.Fatal("disabled destination received HTTP")
	default:
	}
	t.Log("#9: queued Bark disabled before dispatch -> actual SMTP; new Bark preference disabled -> actual SMTP; 2 email deliveries, 0 HTTP")
}

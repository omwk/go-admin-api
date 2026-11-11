package bootstrap

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

type notificationFixtureF3 struct {
	*identityFixture
	manager *notificationManager
	actor   channel.Actor
	router  *gin.Engine
	email   string
	phone   string
	token   string
}

func newNotificationFixtureF3(t *testing.T) *notificationFixtureF3 {
	t.Helper()
	f := newIdentityFixture(t)
	email := hex.EncodeToString(f.secret[:12]) + "@example.invalid"
	phone := "+" + strings.Map(func(r rune) rune { return '0' + r%10 }, hex.EncodeToString(f.secret[:6]))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET is_super=true,email=?,phone=? WHERE id=?", email, phone, f.user.ID))
	s, err := crypto.NewKeyring(map[int][]byte{1: f.secret}, 1)
	require.NoError(t, err)
	m := newNotificationManager(&notificationStore{database: f.database}, s, func() time.Time { return f.now })
	f3 := &notificationFixtureF3{identityFixture: f, manager: m, actor: channel.Actor{TenantID: 1, UserID: f.user.ID, Super: true}, router: gin.New(), email: email, phone: phone}
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	r := &credentialRefreshRuntime{database: f.database, sealer: s, now: m.now}
	require.NoError(t, registerNotificationManagement(f3.router, f.service, profiles, r))
	f3.token = f.login(t).AccessToken
	return f3
}

func (f *notificationFixtureF3) request(t *testing.T, method, path, key string, value any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	r := httptest.NewRequest(method, "/api/v1/admin/notifications"+path, bytes.NewReader(raw)).WithContext(f.ctx)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}

func (f *notificationFixtureF3) provider(t *testing.T, kind, name string, priority int, enabled bool, cfg channel.Config) channel.ProviderView {
	t.Helper()
	ch := map[string]string{"smtp": "email", "sendgrid": "email", "http": "webhook", "aliyun_sms": "sms"}[kind]
	out, err := f.manager.SaveProvider(f.ctx, f.actor, 0, channel.ProviderInput{Channel: ch, Kind: kind, Name: name, Priority: priority, Enabled: enabled, Config: cfg})
	require.NoError(t, err)
	return out.(channel.ProviderView)
}

func (f *notificationFixtureF3) send(t *testing.T, kind string) channel.Delivery {
	t.Helper()
	d, err := f.manager.delivery.Send(f.ctx, 1, channel.Request{Channel: kind, TemplateCode: "notification.default", UserID: f.user.ID, Variables: map[string]string{"Name": "Ada"}})
	require.NoError(t, err)
	return d
}

func (f *notificationFixtureF3) assertPrivacy(t *testing.T, values ...string) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, table := range []string{"channel_provider", "notify_outbox", "notify_delivery", "notification", "audit_log", "idempotency_key"} {
		var raw string
		require.NoError(t, q.Raw("SELECT coalesce(string_agg(row_to_json(x)::text,''),'') FROM "+table+" x").Scan(&raw).Error)
		for _, value := range append(values, f.email, f.phone) {
			require.NotContains(t, raw, value, table)
		}
	}
}

func localSMTPF3(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	sent := &atomic.Int32{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := server.Listener.Accept()
			if err != nil {
				return
			}
			handleSMTPF3(conn, sent)
		}
	}()
	t.Cleanup(func() { server.Close(); <-done })
	return server.Listener.Addr().String(), sent
}

func handleSMTPF3(conn net.Conn, sent *atomic.Int32) {
	defer func() {
		if err := conn.Close(); err != nil {
			return
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return
	}
	reader := bufio.NewReader(conn)
	write := func(text string) bool { _, err := io.WriteString(conn, text); return err == nil }
	if !write("220 local\r\n") {
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			if !write("250 local\r\n") {
				return
			}
		case strings.HasPrefix(line, "DATA"):
			if !write("354 data\r\n") {
				return
			}
			for {
				line, err = reader.ReadString('\n')
				if err != nil {
					return
				}
				if line == ".\r\n" {
					break
				}
			}
			sent.Add(1)
			if !write("250 accepted\r\n") {
				return
			}
		case strings.HasPrefix(line, "QUIT"):
			write("221 bye\r\n")
			return
		default:
			if !write("250 ok\r\n") {
				return
			}
		}
	}
}

func notificationDataF3(t *testing.T, w *httptest.ResponseRecorder, value any) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.NoError(t, json.Unmarshal(envelope.Data, value))
}

func mockAcceptedF3(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(202) }))
	t.Cleanup(server.Close)
	return server, calls
}

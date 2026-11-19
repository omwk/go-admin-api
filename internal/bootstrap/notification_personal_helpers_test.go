package bootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

type personalRoundTrip func(*http.Request) (*http.Response, error)

func (f personalRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func personalRandom(t *testing.T) string {
	t.Helper()
	value := make([]byte, 20)
	_, err := rand.Read(value)
	require.NoError(t, err)
	return hex.EncodeToString(value)
}

// Only tests map validated public destinations onto an actual local HTTP server.
func localPersonalTransport(t *testing.T, m *notificationManager, serverURL string) {
	t.Helper()
	local, err := url.Parse(serverURL)
	require.NoError(t, err)
	m.delivery.Personal.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	m.delivery.Personal.Transport = personalRoundTrip(func(r *http.Request) (*http.Response, error) {
		copyRequest := r.Clone(r.Context())
		copyURL := *r.URL
		copyURL.Scheme, copyURL.Host = local.Scheme, local.Host
		copyRequest.URL, copyRequest.Host = &copyURL, local.Host
		return http.DefaultTransport.RoundTrip(copyRequest)
	})
}

func savePersonalFixture(ctx context.Context, t *testing.T, m *notificationManager, a channel.Actor, kind, serverURL string, verified bool) string {
	t.Helper()
	localPersonalTransport(t, m, serverURL)
	endpoint := "https://personal.example.invalid/" + personalRandom(t)
	_, err := m.SavePersonalChannel(ctx, a, kind, channel.PersonalChannelInput{Endpoint: endpoint}, nil)
	require.NoError(t, err)
	if verified {
		require.NoError(t, m.store.database.Exec(ctx, "UPDATE sys_user_channel SET verified_at=? WHERE tenant_id=? AND user_id=? AND channel=?", m.now(), a.TenantID, a.UserID, kind))
	}
	return endpoint
}

func personalRouter(t *testing.T, f *notificationFixtureF3) *gin.Engine {
	t.Helper()
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	actor, err := identity.RequireActor(profiles)
	require.NoError(t, err)
	access := channel.Access{Authenticate: gin.HandlersChain{identity.JWTAuth(f.service), actor}, Actor: func(c *gin.Context) channel.Actor {
		a := identity.AdminActor(c)
		return channel.Actor{TenantID: a.User.TenantID, UserID: a.User.ID, Super: a.User.IsSuper}
	}}
	router := gin.New()
	channel.RegisterPersonal(router, f.manager, access)
	n := newCredentialNotifications(&credentialRefreshRuntime{database: f.database, sealer: f.manager.sealer, now: f.manager.now})
	notifier.RegisterPersonalPreferences(router, n, notifier.HTTPAccess{Authenticate: access.Authenticate, Actor: func(c *gin.Context) (int64, int64) {
		a := access.Actor(c)
		return a.TenantID, a.UserID
	}})
	return router
}

func personalHTTP(t *testing.T, f *notificationFixtureF3, router *gin.Engine, token, method, path, key string, value any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	r := httptest.NewRequest(method, "/api/v1/auth/"+path, bytes.NewReader(raw)).WithContext(f.ctx)
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	parts := strings.Split(path, "/")
	if parts[0] == "notification-channels" && len(parts) > 1 {
		parts[1] = "{channel}"
	}
	validateOpenAPIResponse(t, readSystemOpenAPI(t), "/api/v1/auth/"+strings.Join(parts, "/"), strings.ToLower(method), w.Code, w.Body.Bytes())
	return w
}

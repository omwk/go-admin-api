package bootstrap

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

type gatewayHTTPFixture struct {
	*orderQueryFixture
	gate      *gatewayStore
	life      *gateway.Service
	resources *gateway.ResourceService
	client    gateway.Client
	key       gateway.IssuedSecret
	grant     gateway.ResourceGrant
	route     gateway.Route
	handler   http.Handler
	orderID   int64
}

func newGatewayHTTPFixture(t *testing.T) *gatewayHTTPFixture {
	t.Helper()
	f := &gatewayHTTPFixture{orderQueryFixture: newOrderQueryFixture(t)}
	f.gate = &gatewayStore{database: f.database}
	var err error
	f.life, err = gateway.NewService(f.gate, f.sealer, func() time.Time { return f.now })
	require.NoError(t, err)
	f.resources, err = gateway.NewResourceService(f.gate)
	require.NoError(t, err)
	f.client, f.key, err = f.life.Create(f.ctx, f.gatewayActor(), "status-reader", f.gatewayActor().UserID, nil)
	require.NoError(t, err)
	f.orderID = f.add(t, "public-status", time.Now().UTC())
	f.grant, err = f.resources.Grant(f.ctx, f.gatewayActor(), f.client.ID, f.orderID)
	require.NoError(t, err)
	c := gateway.Capabilities()[0]
	f.route, err = f.gate.SaveRoute(f.ctx, f.gatewayActor(), gateway.Route{Name: "Order status", Code: c.Code, Capability: c.Code, Method: c.Method, Path: c.Path, RouteGroup: c.Group, Visibility: "public", Status: "enabled", BillingMode: "free"})
	require.NoError(t, err)
	require.NoError(t, f.gate.GrantRoute(f.ctx, f.gatewayActor(), f.client.ID, f.route.ID, "active", nil))
	nonces, err := newGatewayNonces(f.ctx, config.Redis{Addr: f.server.Addr(), PoolSize: 4})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, nonces.Close()) })
	auth, err := gateway.NewAuthenticator(f.gate, nonces, f.sealer, func(*http.Request) string { return "192.0.2.1" }, func() time.Time { return f.now })
	require.NoError(t, err)
	orders, err := NewGatewayOrderReader(f.database)
	require.NoError(t, err)
	open, err := gateway.NewOpenHandler(auth, f.gate, orders, func() time.Time { return f.now })
	require.NoError(t, err)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, gateway.RegisterAdminRoutes(f.router, f.identityFixture.service, profiles, f.life, f.gate, f.resources, newIdempotencyStore(f.database)))
	f.handler = gateway.Dispatch(open, f.router)
	return f
}

func (f *gatewayHTTPFixture) gatewayActor() gateway.Actor {
	return gateway.Actor{TenantID: 1, UserID: f.credentialFixture.actor.UserID}
}
func (f *gatewayHTTPFixture) publicPath() string {
	return "/open/v1/orders/" + f.grant.PublicID + "/status"
}

func (f *gatewayHTTPFixture) sign(t *testing.T, path, text string) *http.Request {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("X-App-Id", f.client.AppID)
	r.Header.Set("X-Timestamp", strconv.FormatInt(f.now.Unix(), 10))
	r.Header.Set("X-Nonce", hex.EncodeToString(shopRandomBytes(t, 16)))
	digest := sha256.Sum256(nil)
	canonical := strings.Join([]string{"GET", r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("X-Timestamp"), r.Header.Get("X-Nonce"), hex.EncodeToString(digest[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(text))
	mac.Write([]byte(canonical))
	r.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return r
}

func (f *gatewayHTTPFixture) serve(t *testing.T, r *http.Request) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
	t.Helper()
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return w, body
}

func (f *gatewayHTTPFixture) manage(t *testing.T, method, path string, payload any, key string) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	if payload == nil {
		raw = nil
	}
	r := httptest.NewRequest(method, "/api/v1/admin"+path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+f.bearer)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	return f.serve(t, r)
}

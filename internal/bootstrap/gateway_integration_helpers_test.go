package bootstrap

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	kernelcrypto "github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

type gatewayFixture struct {
	*identityFixture
	store   *gatewayStore
	service *gateway.Service
	auth    *gateway.Authenticator
	nonces  *gatewayNonces
	client  gateway.Client
	issued  gateway.IssuedSecret
	sealer  kernelcrypto.Sealer
}

func newGatewayFixture(t *testing.T) *gatewayFixture {
	t.Helper()
	f := &gatewayFixture{identityFixture: newIdentityFixture(t)}
	sealer, err := kernelcrypto.NewKeyring(map[int][]byte{1: shopRandomBytes(t, 32)}, 1)
	require.NoError(t, err)
	f.sealer = sealer
	f.store = &gatewayStore{database: f.database}
	f.service, err = gateway.NewService(f.store, sealer, func() time.Time { return f.now })
	require.NoError(t, err)
	f.client, f.issued, err = f.service.Create(f.ctx, f.actor(), "gateway-integration", f.user.ID, nil)
	require.NoError(t, err)
	f.nonces, err = newGatewayNonces(f.ctx, config.Redis{Addr: f.server.Addr(), PoolSize: 4})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.nonces.Close()) })
	f.auth, err = gateway.NewAuthenticator(f.store, f.nonces, sealer, func(*http.Request) string { return "192.0.2.1" }, func() time.Time { return f.now })
	require.NoError(t, err)
	return f
}

type gatewayAuditFailure struct{ *gatewayStore }

func (*gatewayAuditFailure) Audit(context.Context, audit.Event) error {
	return errors.New("injected audit failure")
}

func (f *gatewayFixture) actor() gateway.Actor {
	return gateway.Actor{TenantID: f.user.TenantID, UserID: f.user.ID}
}

func (f *gatewayFixture) signed(t *testing.T, text string) *http.Request {
	t.Helper()
	r := httptest.NewRequest("GET", "/open/v1/orders/example/status", nil)
	r.Header.Set("X-App-Id", f.client.AppID)
	r.Header.Set("X-Timestamp", strconv.FormatInt(f.now.Unix(), 10))
	r.Header.Set("X-Nonce", hex.EncodeToString(shopRandomBytes(t, 16)))
	digest := sha256.Sum256(nil)
	canonical := strings.Join([]string{"GET", r.URL.EscapedPath(), "", r.Header.Get("X-Timestamp"), r.Header.Get("X-Nonce"), hex.EncodeToString(digest[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(text))
	mac.Write([]byte(canonical))
	r.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return r
}

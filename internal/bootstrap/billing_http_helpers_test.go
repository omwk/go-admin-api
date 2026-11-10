package bootstrap

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

type billingHTTPFixture struct {
	*gatewayHTTPFixture
	money   *billing.Service
	wallet  billing.Wallet
	charges *gatewayBilling
	calls   *gateway.CallService
}

func newBillingHTTPFixture(t *testing.T, balance int64) *billingHTTPFixture {
	t.Helper()
	f := &billingHTTPFixture{gatewayHTTPFixture: newGatewayHTTPFixture(t)}
	var err error
	f.charges, err = newGatewayBilling(f.database, func() time.Time { return f.now })
	require.NoError(t, err)
	f.money = f.charges.money
	a := billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}
	f.wallet, err = f.money.Open(f.ctx, a, f.client.AppID)
	require.NoError(t, err)
	if balance > 0 {
		f.wallet, err = f.money.Topup(f.ctx, a, f.wallet.ID, balance, "initial", "initial")
		require.NoError(t, err)
	}
	f.route.BillingMode, f.route.PriceCents, f.route.UnitsPerCall, f.route.Currency = "per_call", 5, 1, billing.Currency
	f.route, err = f.gate.SaveRoute(f.ctx, f.gatewayActor(), f.route)
	require.NoError(t, err)
	f.calls, err = gateway.NewCallService(f.gate, func() time.Time { return f.now }, gateway.CallPolicy{Timeout: time.Second, MaxAttempts: 2, HashKey: shopRandomBytes(t, 32)}, f.charges)
	require.NoError(t, err)
	f.handler = f.open(t, nil)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, billing.RegisterAdminRoutes(f.router, f.identityFixture.service, profiles, f.money, newIdempotencyStore(f.database)))
	f.handler = gateway.Dispatch(f.handler, f.router)
	return f
}

type billingPassLimiter struct{}

func (billingPassLimiter) Take(_ context.Context, _ gateway.Principal, _ int64) (gateway.RateDecision, error) {
	return gateway.RateDecision{}, nil
}

func (f *billingHTTPFixture) open(t *testing.T, reader gateway.OrderStatusReader) http.Handler {
	t.Helper()
	nonces, err := newGatewayNonces(f.ctx, config.Redis{Addr: f.server.Addr(), PoolSize: 8})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, nonces.Close()) })
	auth, err := gateway.NewAuthenticator(f.gate, nonces, f.sealer, func(*http.Request) string { return "192.0.2.1" }, func() time.Time { return f.now })
	require.NoError(t, err)
	if reader == nil {
		reader, err = NewGatewayOrderReader(f.database)
		require.NoError(t, err)
	}
	h, err := gateway.NewOpenHandler(auth, f.gate, reader, f.calls, billingPassLimiter{}, func() time.Time { return f.now }, f.charges)
	require.NoError(t, err)
	return h
}

func (f *billingHTTPFixture) signed(t *testing.T, method, path, key string, payload any) *http.Request {
	t.Helper()
	var raw []byte
	if payload != nil {
		var err error
		raw, err = json.Marshal(payload)
		require.NoError(t, err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("X-App-Id", f.client.AppID)
	r.Header.Set("X-Timestamp", strconv.FormatInt(f.now.Unix(), 10))
	r.Header.Set("X-Nonce", hex.EncodeToString(shopRandomBytes(t, 16)))
	r.Header.Set("X-Signature-Version", "2")
	r.Header.Set("Idempotency-Key", key)
	canonical, err := gateway.SigningRequest(r, r.Header.Get("X-Timestamp"), r.Header.Get("X-Nonce"), raw)
	require.NoError(t, err)
	mac := hmac.New(sha256.New, []byte(f.key.Secret))
	mac.Write([]byte(canonical))
	r.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return r
}

func (f *billingHTTPFixture) intent() gateway.Call {
	return f.calls.NewCall(gateway.Principal{TenantID: 1, ClientID: f.client.ID, AppID: f.client.AppID}, f.route, "trace", "request", "192.0.2.1")
}

func (f *billingHTTPFixture) assertFunds(t *testing.T, balance, frozen int64) {
	t.Helper()
	w, err := f.money.Balance(f.ctx, 1, f.client.AppID)
	require.NoError(t, err)
	require.Equal(t, balance, w.BalanceCents)
	require.Equal(t, frozen, w.FrozenCents)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var sum struct{ Balance, Frozen int64 }
	require.NoError(t, q.Table("wallet_txn").Select("coalesce(sum(balance_delta_cents),0) AS balance,coalesce(sum(frozen_delta_cents),0) AS frozen").Where("wallet_id=?", f.wallet.ID).Scan(&sum).Error)
	require.Equal(t, w.BalanceCents, sum.Balance)
	require.Equal(t, w.FrozenCents, sum.Frozen)
}

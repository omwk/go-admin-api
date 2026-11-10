package bootstrap

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func TestBillingHTTPIntegrationSignedReplayAndReceipt(t *testing.T) {
	// REQ-BILL-003: retries use the first price and durable result, even after repricing.
	f := newBillingHTTPFixture(t, 100)
	w, body := f.serve(t, f.signed(t, "GET", f.publicPath(), "one-call", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	id := w.Header().Get("X-Call-Id")
	original := body["data"]
	f.route.PriceCents = 7
	var err error
	f.route, err = f.gate.SaveRoute(f.ctx, f.gatewayActor(), f.route)
	require.NoError(t, err)
	w, body = f.serve(t, f.signed(t, "GET", f.publicPath(), "one-call", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, id, w.Header().Get("X-Call-Id"))
	require.JSONEq(t, string(original), string(body["data"]))
	f.assertFunds(t, 95, 0)
	w, body = f.serve(t, f.signed(t, "GET", "/open/v1/orders/"+ulid.Make().String()+"/status", "one-call", nil))
	require.Equal(t, 409, w.Code, w.Body.String())
	require.JSONEq(t, "10006", string(body["code"]))
	f.assertFunds(t, 95, 0)
	w, body = f.serve(t, f.signed(t, "GET", "/open/v1/calls/"+id+"/result", "receipt", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var receipt struct {
		CallID      string `json:"call_id"`
		AmountCents int64  `json:"amount_cents"`
	}
	require.NoError(t, json.Unmarshal(body["data"], &receipt))
	require.Equal(t, id, receipt.CallID)
	require.EqualValues(t, 5, receipt.AmountCents)
	r := f.signed(t, "GET", f.publicPath(), "one-call", nil)
	r.Header.Set("Idempotency-Key", "tampered")
	w, _ = f.serve(t, r)
	require.Equal(t, 401, w.Code)
	r = f.signed(t, "GET", f.publicPath(), "one-call", nil)
	r.URL.RawQuery = "x=1"
	w, _ = f.serve(t, r)
	require.Equal(t, 401, w.Code)
	// Revoking the per-order ceiling also revokes access to the persisted receipt.
	require.NoError(t, f.resources.Revoke(f.ctx, f.gatewayActor(), f.client.ID, f.grant.ID, f.grant.Version))
	w, _ = f.serve(t, f.signed(t, "GET", "/open/v1/calls/"+id+"/result", "revoked", nil))
	require.Equal(t, 404, w.Code)
	w, _ = f.serve(t, f.signed(t, "GET", f.publicPath(), "one-call", nil))
	require.Equal(t, 404, w.Code)
	f.assertFunds(t, 95, 0)
}

func TestBillingHTTPIntegrationRechargeManagementAndRedeem(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	path := "/wallets/" + strconv.FormatInt(f.wallet.ID, 10) + "/recharge"
	for i := 0; i < 2; i++ {
		w, _ := f.manage(t, "POST", path, map[string]any{"amount_cents": 50, "note": "funding"}, "one-topup")
		require.Equal(t, 201, w.Code, w.Body.String())
	}
	w, _ := f.manage(t, "POST", path, map[string]any{"amount_cents": 51, "note": "funding"}, "one-topup")
	require.Equal(t, 409, w.Code)
	w, body := f.manage(t, "POST", "/recharge-codes", map[string]any{"amount_cents": 25, "count": 1}, "one-code")
	require.Equal(t, 201, w.Code, w.Body.String())
	var codes []billing.CodeDelivery
	require.NoError(t, json.Unmarshal(body["data"], &codes))
	require.Len(t, codes, 1)
	w, body = f.manage(t, "POST", "/recharge-codes", map[string]any{"amount_cents": 25, "count": 1}, "one-code")
	require.Equal(t, 201, w.Code)
	require.JSONEq(t, `{"delivered":true}`, string(body["data"]))
	w, _ = f.serve(t, f.signed(t, "POST", "/open/v1/recharge-code", "redeem", map[string]any{"code": codes[0].Code}))
	require.Equal(t, 200, w.Code, w.Body.String())
	w, _ = f.serve(t, f.signed(t, "POST", "/open/v1/recharge-code", "redeem", map[string]any{"code": codes[0].Code}))
	require.Equal(t, 400, w.Code)
	f.assertFunds(t, 175, 0)
	w, body = f.serve(t, f.signed(t, "GET", "/open/v1/balance", "balance", nil))
	require.Equal(t, 200, w.Code)
	var balance gateway.OpenBalance
	require.NoError(t, json.Unmarshal(body["data"], &balance))
	require.EqualValues(t, 175, balance.AvailableCents)
	require.Equal(t, "CNY", balance.Currency)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var raw string
	require.NoError(t, q.Table("idempotency_key").Select("string_agg(response_body::text,'')").Where("scope LIKE ?", "%recharge-codes%").Scan(&raw).Error)
	require.NotContains(t, raw, codes[0].Code)
	w, body = f.manage(t, "GET", "/wallets", nil, "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, string(body["data"]), "available_cents")
}

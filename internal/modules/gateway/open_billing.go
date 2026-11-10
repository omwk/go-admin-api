package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/oklog/ulid/v2"
)

// OpenBalance is independent of the financial storage model.
type OpenBalance struct {
	Currency       string `json:"currency"`
	BalanceCents   int64  `json:"balance_cents"`
	AvailableCents int64  `json:"available_cents"`
	FrozenCents    int64  `json:"frozen_cents"`
}

// WalletPort exposes only the authenticated application's own cash account.
type WalletPort interface {
	Balance(context.Context, Principal) (OpenBalance, error)
	Redeem(context.Context, Principal, string) (OpenBalance, error)
}

func (h *OpenHandler) financial(w http.ResponseWriter, r *http.Request, traceID, requestID string) {
	p, err := h.auth.Authenticate(r.Context(), r)
	if err != nil {
		h.write(w, traceID, requestID, nil, err)
		return
	}
	decision, err := h.limits.Take(r.Context(), p, 0)
	writeRateHeaders(w, decision)
	if err != nil {
		h.write(w, traceID, requestID, nil, err)
		return
	}
	if r.URL.RawQuery != "" {
		h.write(w, traceID, requestID, nil, Invalid())
		return
	}
	if strings.HasPrefix(r.URL.Path, "/open/v1/calls/") {
		h.receipt(w, r, p, traceID, requestID)
		return
	}
	if h.wallets == nil {
		h.write(w, traceID, requestID, nil, Unavailable())
		return
	}
	var data OpenBalance
	if r.Method == "GET" {
		data, err = h.wallets.Balance(r.Context(), p)
	} else {
		var in struct {
			Code string `json:"code"`
		}
		body, readErr := boundedBody(r)
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if readErr != nil || decoder.Decode(&in) != nil || decoder.Decode(&struct{}{}) != io.EOF || r.Header.Get("X-Signature-Version") != "2" {
			h.write(w, traceID, requestID, nil, Invalid())
			return
		}
		data, err = h.wallets.Redeem(r.Context(), p, in.Code)
	}
	if err == nil {
		err = h.auth.store.Touch(r.Context(), p, h.now().UTC())
	}
	h.write(w, traceID, requestID, data, err)
}

func (h *OpenHandler) receipt(w http.ResponseWriter, r *http.Request, p Principal, traceID, requestID string) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 6 || parts[5] != "result" {
		h.write(w, traceID, requestID, nil, RouteUnavailable())
		return
	}
	id := parts[4]
	if _, err := ulid.ParseStrict(id); err != nil {
		h.write(w, traceID, requestID, nil, ResourceUnavailable())
		return
	}
	c, err := h.calls.store.Call(r.Context(), id)
	if err == nil && (c.TenantID != p.TenantID || c.ClientID != p.ClientID) {
		err = ResourceUnavailable()
	}
	if err != nil {
		h.write(w, traceID, requestID, nil, err)
		return
	}
	data, err := replayCall(c)
	if err == nil {
		err = h.calls.confirmCharge(r.Context(), c)
	}
	if err == nil {
		err = h.authorizeReceipt(r, p, c, data)
	}
	if err != nil {
		h.write(w, traceID, requestID, nil, err)
		return
	}
	w.Header().Set("X-Call-Id", id)
	out := struct {
		CallID      string          `json:"call_id"`
		State       string          `json:"state"`
		Result      json.RawMessage `json:"result"`
		Currency    string          `json:"currency"`
		AmountCents int64           `json:"amount_cents"`
	}{id, c.State, data, c.Currency, c.CostCents}
	h.write(w, traceID, requestID, out, nil)
}

func (h *OpenHandler) authorizeReceipt(r *http.Request, p Principal, c Call, data json.RawMessage) error {
	capability := Capabilities()[0]
	route, err := h.routes.Route(r.Context(), p, capability.Code)
	if err != nil {
		return err
	}
	if route.ID != c.RouteID {
		return ResourceUnavailable()
	}
	var result OpenOrderStatus
	if json.Unmarshal(data, &result) != nil {
		return Unavailable()
	}
	if _, err := h.orders.OrderStatus(r.Context(), p, result.ID); err != nil {
		return err
	}
	return h.recheck(r, p, route, capability)
}

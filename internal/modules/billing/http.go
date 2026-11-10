package billing

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// AdminHandler exposes funding through human JWT and registered permissions.
type AdminHandler struct {
	money    *Service
	requests idem.Store
}

// RegisterAdminRoutes publishes only M9a wallet and recharge operations.
func RegisterAdminRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, money *Service, requests idem.Store, reports ...*Reconciler) error {
	if router == nil || auth == nil || profiles == nil || money == nil || requests == nil {
		return errors.New("billing management dependencies required")
	}
	h := &AdminHandler{money: money, requests: requests}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	for _, route := range []struct{ method, path, perm string }{
		{"GET", "/wallets", permission.BillingWalletList}, {"GET", "/wallets/:id", permission.BillingWalletList},
		{"GET", "/wallets/:id/txns", permission.BillingWalletList}, {"POST", "/wallets", permission.BillingWalletRecharge},
		{"POST", "/wallets/:id/recharge", permission.BillingWalletRecharge}, {"POST", "/recharge-codes", permission.BillingWalletRecharge},
	} {
		guard, err := identity.RequirePerm(profiles, route.perm)
		if err != nil {
			return err
		}
		group.Handle(route.method, route.path, guard, h.handle)
	}
	return registerBenefitRoutes(group, profiles, money, requests, reports...)
}

type walletInput struct {
	AppID       string     `json:"app_id"`
	AmountCents int64      `json:"amount_cents"`
	Note        string     `json:"note"`
	Count       int        `json:"count"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

func (h *AdminHandler) handle(c *gin.Context) {
	user := identity.AdminActor(c).User
	a := Actor{TenantID: user.TenantID, UserID: user.ID}
	id := int64(0)
	if c.Param("id") != "" {
		var err error
		id, err = strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			httpx.Fail(c, Invalid())
			return
		}
	}
	if c.Request.Method == "GET" {
		h.read(c, a, id)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		httpx.Fail(c, Invalid())
		return
	}
	var in walletInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		httpx.Fail(c, Invalid())
		return
	}
	out, status, err := h.execute(c.Request.Context(), a, id, c.FullPath(), in, idem.Input{Key: c.GetHeader("Idempotency-Key"), Method: c.Request.Method, Route: c.FullPath(), PathParams: []string{c.Param("id")}, Body: raw})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	if status == 201 {
		httpx.Created(c, out)
	} else {
		httpx.OK(c, out)
	}
}

func (h *AdminHandler) read(c *gin.Context, a Actor, id int64) {
	ctx := c.Request.Context()
	var out any
	var err error
	switch c.FullPath() {
	case "/api/v1/admin/wallets":
		var wallets []Wallet
		wallets, err = h.money.Wallets(ctx, a)
		views := make([]walletView, 0, len(wallets))
		for _, w := range wallets {
			views = append(views, viewWallet(w))
		}
		out = views
	case "/api/v1/admin/wallets/:id":
		var w Wallet
		w, err = h.money.store.OwnedWallet(ctx, a, id)
		out = viewWallet(w)
	default:
		var txns []Txn
		txns, err = h.money.Txns(ctx, a, id)
		views := make([]txnView, 0, len(txns))
		for _, txn := range txns {
			views = append(views, viewTxn(txn))
		}
		out = views
	}
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	httpx.OK(c, out)
}

type walletView struct {
	Wallet
	AvailableCents int64 `json:"available_cents"`
}

func viewWallet(w Wallet) walletView {
	return walletView{Wallet: w, AvailableCents: w.BalanceCents - w.FrozenCents}
}

type txnView struct {
	ID                int64     `json:"id"`
	Type              string    `json:"type"`
	AmountCents       int64     `json:"amount_cents"`
	Currency          string    `json:"currency"`
	BalanceAfterCents int64     `json:"balance_after_cents"`
	FrozenAfterCents  int64     `json:"frozen_after_cents"`
	CallID            string    `json:"call_id,omitempty"`
	OccurredAt        time.Time `json:"occurred_at"`
}

func viewTxn(t Txn) txnView {
	return txnView{t.ID, t.Type, t.AmountCents, Currency, t.BalanceAfterCents, t.FrozenAfterCents, t.CallID, t.OccurredAt}
}

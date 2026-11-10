package billing

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

func (h *AdminHandler) execute(ctx context.Context, a Actor, id int64, path string, in walletInput, input idem.Input) (any, int, error) {
	if input.Key == "" {
		return nil, 0, Invalid()
	}
	op, err := idem.Prepare(a.TenantID, a.UserID, input)
	if err != nil {
		return nil, 0, Invalid()
	}
	var out any
	status := 201
	err = h.money.store.Transaction(ctx, func(tx context.Context) error {
		claim, err := h.requests.Claim(tx, op)
		if err != nil {
			return billingIdemError(err)
		}
		if claim.Action == idem.Replay {
			out, status = claim.Result.Body, claim.Result.Status
			return nil
		}
		out, err = h.write(tx, a, id, path, in, input.Key)
		if err != nil {
			return err
		}
		safe := out
		if path == "/api/v1/admin/recharge-codes" {
			safe = map[string]any{"delivered": true}
		}
		result, err := idem.JSONResult(status, safe)
		if err != nil {
			return err
		}
		_, err = h.requests.Complete(tx, op, result)
		return billingIdemError(err)
	})
	return out, status, err
}

func (h *AdminHandler) write(ctx context.Context, a Actor, id int64, path string, in walletInput, key string) (any, error) {
	switch path {
	case "/api/v1/admin/wallets":
		w, err := h.money.Open(ctx, a, in.AppID)
		return viewWallet(w), err
	case "/api/v1/admin/wallets/:id/recharge":
		w, err := h.money.Topup(ctx, a, id, in.AmountCents, in.Note, key)
		return viewWallet(w), err
	default:
		return h.money.Mint(ctx, a, in.AmountCents, in.Count, in.ExpiresAt)
	}
}

func billingIdemError(err error) error {
	if errors.Is(err, idem.ErrMismatch) {
		return Mismatch()
	}
	if errors.Is(err, idem.ErrProcessing) {
		return errs.New(10004, 409, "请求正在处理")
	}
	return err
}

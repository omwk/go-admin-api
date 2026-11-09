package gateway

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

func (h *AdminHandler) execute(ctx context.Context, a Actor, r AdminRoute, id int64, sid, gid string, in adminInput, input idem.Input) (any, int, error) {
	var out any
	var status int
	err := h.lifecycle.store.Transaction(ctx, func(tx context.Context) error {
		var operation idem.Operation
		if input.Key != "" {
			var err error
			operation, err = idem.Prepare(a.TenantID, a.UserID, input)
			if err != nil {
				return Invalid()
			}
			claim, err := h.requests.Claim(tx, operation)
			if err != nil {
				return adminIdempotencyError(err)
			}
			if claim.Action == idem.Replay {
				out = claim.Result.Body
				status = claim.Result.Status
				return nil
			}
		}
		var err error
		out, status, err = h.write(tx, a, r, id, sid, gid, in)
		if err != nil {
			return err
		}
		if input.Key == "" {
			return nil
		}
		safe := out
		if r.Path == "/api-clients" || r.Path == "/api-clients/:id/secrets" {
			// Only a delivery receipt is persisted; plaintext never enters replay storage.
			safe = map[string]any{"delivered": true}
		}
		result, err := idem.JSONResult(status, safe)
		if err != nil {
			return err
		}
		_, err = h.requests.Complete(tx, operation, result)
		return adminIdempotencyError(err)
	})
	return out, status, err
}

func adminIdempotencyError(err error) error {
	if errors.Is(err, idem.ErrMismatch) {
		return errs.New(10006, 409, "幂等键与请求不一致")
	}
	if errors.Is(err, idem.ErrProcessing) {
		return errs.New(10004, 409, "请求正在处理")
	}
	return err
}

package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
)

func (s *categoryManagementStore) categoryMutation(ctx context.Context, tenant int64, operations []*idem.Operation, fn func(context.Context) error) error {
	var op *idem.Operation
	if len(operations) > 0 {
		op = operations[0]
	}
	if op != nil {
		human, err := identityscope.From(ctx)
		if err != nil {
			return err
		}
		if op.TenantID != tenant || human.Tenant != tenant || op.UserID != human.User {
			return identityscope.Denied()
		}
	}
	err := s.database.Transaction(ctx, func(ctx context.Context) error {
		store := newIdempotencyStore(s.database)
		if op != nil {
			claim, err := store.Claim(ctx, *op)
			if err != nil {
				return err
			}
			if claim.Action == idem.Replay {
				return nil
			}
		}
		if err := fn(ctx); err != nil {
			return err
		}
		if op == nil {
			return nil
		}
		result, err := idem.JSONResult(200, map[string]bool{"ok": true})
		if err != nil {
			return err
		}
		_, err = store.Complete(ctx, *op, result)
		return err
	})
	if errors.Is(err, idem.ErrMismatch) {
		return errs.New(errs.ErrIdempotencyMismatch, 409, "幂等请求不一致")
	}
	if errors.Is(err, idem.ErrProcessing) {
		return errs.New(errs.ErrConflict, 409, "请求处理中")
	}
	return err
}

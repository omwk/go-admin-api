package billing

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

func (h *benefitHandler) execute(ctx context.Context, a Actor, id int64, path string, raw []byte, in idem.Input) (any, error) {
	if in.Key == "" {
		return nil, Invalid()
	}
	if path == "/api/v1/admin/billing-reconciliations" {
		if h.reports == nil {
			return nil, Unavailable()
		}
		var input struct {
			ServiceDate string `json:"service_date"`
		}
		if err := decodeBenefit(raw, &input); err != nil {
			return nil, err
		}
		return h.reports.Manual(ctx, a, input.ServiceDate, in.Key)
	}
	op, err := idem.Prepare(a.TenantID, a.UserID, in)
	if err != nil {
		return nil, Invalid()
	}
	var out any
	err = h.money.store.Transaction(ctx, func(tx context.Context) error {
		claim, err := h.requests.Claim(tx, op)
		if err != nil {
			return billingIdemError(err)
		}
		if claim.Action == idem.Replay {
			out = claim.Result.Body
			return nil
		}
		out, err = h.write(tx, a, id, path, raw, in.Key)
		if err != nil {
			return err
		}
		result, err := idem.JSONResult(201, out)
		if err != nil {
			return err
		}
		_, err = h.requests.Complete(tx, op, result)
		return billingIdemError(err)
	})
	return out, err
}

func (h *benefitHandler) write(ctx context.Context, a Actor, id int64, path string, raw []byte, key string) (any, error) {
	switch path {
	case "/api/v1/admin/pricing-plans", "/api/v1/admin/pricing-plans/:id":
		var in Plan
		if err := decodeBenefit(raw, &in); err != nil {
			return nil, err
		}
		in.ID = id
		return h.money.SavePlan(ctx, a, in)
	case "/api/v1/admin/subscriptions":
		var in GrantInput
		if err := decodeBenefit(raw, &in); err != nil {
			return nil, err
		}
		return h.money.Grant(ctx, a, in, key)
	default:
		if h.reports == nil {
			return nil, Unavailable()
		}
		var in struct {
			Reason string `json:"reason"`
		}
		if err := decodeBenefit(raw, &in); err != nil {
			return nil, err
		}
		return h.reports.Compensate(ctx, a, id, in.Reason, key)
	}
}

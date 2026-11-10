package gateway

import (
	"context"
	"math"
)

// Charges joins the gateway's transaction context without exposing a database handle.
type Charges interface {
	Prepare(context.Context, Call) error
	Freeze(context.Context, Call) error
	Finish(context.Context, Call) error
	Confirm(context.Context, Call) error
}

func validPricing(r Route) bool {
	if r.BillingMode == "free" {
		return r.PriceCents == 0
	}
	return r.BillingMode == "per_call" && r.Currency == "CNY" && r.PriceCents > 0 && r.UnitsPerCall > 0 && r.PriceCents <= math.MaxInt64/r.UnitsPerCall
}

func (s *CallService) prepareCharge(ctx context.Context, c Call) error {
	if c.BillingMode != "per_call" || !c.Admitted {
		return nil
	}
	if s.charges == nil {
		return Unavailable()
	}
	return s.charges.Prepare(ctx, c)
}

func (s *CallService) freezeCharge(ctx context.Context, c Call) error {
	if c.BillingMode != "per_call" || !c.Admitted {
		return nil
	}
	if s.charges == nil {
		return Unavailable()
	}
	return s.charges.Freeze(ctx, c)
}

func (s *CallService) finishCharge(ctx context.Context, c Call) error {
	if c.BillingMode != "per_call" || !c.Admitted {
		return nil
	}
	if s.charges == nil {
		return Unavailable()
	}
	return s.charges.Finish(ctx, c)
}

func (s *CallService) confirmCharge(ctx context.Context, c Call) error {
	if c.BillingMode != "per_call" || !c.Admitted {
		return nil
	}
	if s.charges == nil {
		return Unavailable()
	}
	return s.charges.Confirm(ctx, c)
}

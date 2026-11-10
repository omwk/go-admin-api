package billing

import (
	"context"
	"strings"
)

// Adjust appends an evidenced refund or an open supplemental debit.
func (s *StatementService) Adjust(ctx context.Context, a Actor, in AdjustmentInput, key string) (Adjustment, error) {
	if a.UserID <= 0 || in.OriginalTxnID <= 0 || in.AmountCents <= 0 || in.Kind != "refund" && in.Kind != "supplement" || in.Kind == "supplement" && in.DiffID <= 0 || in.DiffID < 0 || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 500 || strings.TrimSpace(in.Evidence) == "" || len(in.Evidence) > 500 || key == "" || len(key) > 128 {
		return Adjustment{}, Invalid()
	}
	var out Adjustment
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		var err error
		out, err = s.store.Adjust(tx, a, in, fingerprint(a, key), fingerprint(in), s.now().UTC())
		return err
	})
	return out, err
}

// ApplyAdjustment retries the same open debt after funds become available.
func (s *StatementService) ApplyAdjustment(ctx context.Context, a Actor, id int64) (Adjustment, error) {
	if id <= 0 || a.UserID <= 0 {
		return Adjustment{}, Invalid()
	}
	var out Adjustment
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		var err error
		out, err = s.store.ApplyAdjustment(tx, a, id, s.now().UTC())
		return err
	})
	return out, err
}

// Adjustments preserves visibility of open obligations as well as applied entries.
func (s *StatementService) Adjustments(ctx context.Context, a Actor, f StatementFilter) (AdjustmentPage, error) {
	f, err := normalizeStatementFilter(f)
	if err != nil {
		return AdjustmentPage{}, err
	}
	return s.store.Adjustments(ctx, a, f)
}

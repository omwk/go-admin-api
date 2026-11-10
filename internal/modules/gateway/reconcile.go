package gateway

import (
	"context"
	"errors"
	"time"
)

// CallReconciler can only fail expired intents; it cannot create or meter calls.
type CallReconciler struct{ calls CallService }

// NewCallReconciler requires no observation key because failure recovery hashes no results.
func NewCallReconciler(store CallStore, now func() time.Time, charges ...Charges) (*CallReconciler, error) {
	if store == nil || now == nil {
		return nil, errors.New("gateway reconciliation dependencies are required")
	}
	s := CallService{store: store, now: now}
	if len(charges) > 0 {
		s.charges = charges[0]
	}
	return &CallReconciler{calls: s}, nil
}

// Reconcile uses the same transactional terminal transition as request execution.
func (r *CallReconciler) Reconcile(ctx context.Context) (int, error) {
	return r.calls.Reconcile(ctx)
}

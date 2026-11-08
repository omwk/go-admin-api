package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
)

// Called only while M2 holds shop then credential locks in its mutation transaction.
func (s *shopStore) fenceCredentialRefresh(ctx context.Context, row *model.Credential) error {
	now := time.Now().UTC()
	store := &credentialRefreshStore{database: s.database}
	q, cancel := s.raw(ctx)
	defer cancel()
	var runs []model.RefreshRun
	if err := q.Select(refreshRunColumns).Where("tenant_id=? AND credential_id=? AND status='running'", row.TenantID, row.ID).Find(&runs).Error; err != nil {
		return credentialDBError(err)
	}
	for _, run := range runs {
		run.Status, run.Reason, run.FinishedAt, run.UpdatedAt = "skipped", "superseded", &now, now
		if run.Phase == refresh.PhaseSending || run.Phase == refresh.PhaseUnknown {
			run.Phase, run.FailureClass = refresh.PhaseUnknown, refresh.FailureUnknown
			if err := store.Reconcile(ctx, run, now); err != nil {
				return err
			}
		}
		if err := store.SaveRun(ctx, run); err != nil {
			return err
		}
	}
	row.RefreshGeneration++
	if row.Status != "disabled" {
		row.RetryBudget, row.ConsecutiveFailures = refresh.MaxAttempts, 0
		row.RefreshAt = nil
	}
	return nil
}

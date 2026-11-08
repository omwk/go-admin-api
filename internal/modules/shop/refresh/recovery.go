package refresh

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
)

func (e *Executor) markUnknown(ctx context.Context, run *model.RefreshRun, reason string) error {
	if run.Phase == PhaseUnknown {
		return e.store.Reconcile(ctx, *run, e.now().UTC())
	}
	run.Phase, run.FailureClass, run.Reason, run.UpdatedAt = PhaseUnknown, FailureUnknown, reason, e.now().UTC()
	if err := e.store.SaveRun(ctx, *run); err != nil {
		return err
	}
	if err := e.store.Reconcile(ctx, *run, e.now().UTC()); err != nil {
		return err
	}
	return e.store.Audit(ctx, *run, "refresh", "failure", e.now().UTC())
}

func (e *Executor) unknown(ctx context.Context, target Target, id int64, reason string) (model.RefreshRun, error) {
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.config.FinishTimeout)
	defer cancel()
	run, err := e.withRun(finish, target, id, func(tx context.Context, _ Snapshot, run *model.RefreshRun) error {
		if run.Phase == PhasePublished || run.Phase == PhaseConsumed {
			return nil
		}
		return e.markUnknown(tx, run, reason)
	})
	if err != nil {
		return run, ErrPersistence
	}
	if run.Phase != PhaseUnknown {
		return run, nil
	}
	return run, ErrResultUnknown
}

func (e *Executor) finishFailure(ctx context.Context, target Target, id int64, cause error) (model.RefreshRun, error) {
	class := Classify(cause)
	if class == FailureUnknown {
		return e.unknown(ctx, target, id, "ambiguous")
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.config.FinishTimeout)
	defer cancel()
	run, err := e.withRun(finish, target, id, func(tx context.Context, snap Snapshot, run *model.RefreshRun) error {
		if run.Status != "running" || run.Phase != PhaseSending {
			return nil
		}
		if !eligible(snap, *run) {
			return e.supersede(tx, run)
		}
		now := e.now().UTC()
		run.Status, run.FailureClass, run.Reason, run.FinishedAt, run.UpdatedAt = "failed", class, class, &now, now
		c := snap.Credential
		if class == FailureInvalid {
			c.Status = "needs_reauth"
		} else {
			var failure *port.Failure
			errors.As(cause, &failure)
			next, err := e.config.NextRetry(now, run.Attempt, failure.RetryAfter)
			if err != nil {
				return err
			}
			c.ConsecutiveFailures++
			c.RetryBudget--
			c.Status = ProjectStatus(now, c.ExpiresAt)
			run.NextRetryAt = &next
		}
		if err := e.store.SaveCredential(tx, c); err != nil {
			return err
		}
		if err := e.store.SaveRun(tx, *run); err != nil {
			return err
		}
		return e.store.Audit(tx, *run, "refresh", "failure", now)
	})
	if err != nil {
		return e.unknown(ctx, target, id, "stage_failed")
	}
	return run, nil
}

// Recover waits out a live dispatch budget, then records ambiguity without resending.
// Intent may dispatch; consumed may publish; published and unknown are read-only.
func (e *Executor) Recover(ctx context.Context, target Target, id int64) (model.RefreshRun, error) {
	run, err := e.withRun(ctx, target, id, func(tx context.Context, _ Snapshot, r *model.RefreshRun) error {
		if r.Phase == PhaseSending && r.Status == "running" && (r.SendDeadline == nil || !r.SendDeadline.After(e.now())) {
			return e.markUnknown(tx, r, "sending_lost")
		}
		return nil
	})
	if err != nil {
		return run, err
	}
	if run.Status != "running" {
		return run, nil
	}
	if run.Phase == PhaseIntent {
		return e.Execute(ctx, target, id)
	}
	if run.Phase == PhaseConsumed {
		return e.Publish(ctx, target, id)
	}
	return run, nil
}

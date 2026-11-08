package refresh

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/shop/credential"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
)

// Executor advances durable attempts; it has no scheduler, queue, or global identity.
type Executor struct {
	store    Store
	sealer   crypto.Sealer
	platform port.Refresher
	config   Config
	now      func() time.Time
}

// New constructs an unconnected core with explicit dependencies and a testable clock.
func New(store Store, sealer crypto.Sealer, platform port.Refresher, cfg Config, now func() time.Time) (*Executor, error) {
	if store == nil || sealer == nil || platform == nil || now == nil || cfg.CallTimeout <= 0 || cfg.FinishTimeout <= 0 {
		return nil, ErrIneligible
	}
	for i := 1; i <= MaxAttempts; i++ {
		if _, err := cfg.NextRetry(now(), i, time.Time{}); err != nil {
			return nil, err
		}
	}
	return &Executor{store: store, sealer: sealer, platform: platform, config: cfg, now: now}, nil
}

// Prepare deduplicates an observed version and persists intent without sending.
func (e *Executor) Prepare(ctx context.Context, target Target) (model.RefreshRun, error) {
	var result model.RefreshRun
	if target.TenantID <= 0 || target.CredentialID <= 0 || target.InputVersionID <= 0 {
		return result, ErrIneligible
	}
	err := e.store.Transaction(ctx, func(tx context.Context) error {
		snap, err := e.store.Lock(tx, target)
		if err != nil {
			return err
		}
		previous, err := e.store.FindRun(tx, target, 0)
		if err != nil {
			return err
		}
		now := e.now().UTC()
		if previous != nil {
			result = *previous
			if previous.Status != "failed" || previous.FailureClass != FailureTemporary ||
				previous.NextRetryAt == nil || previous.NextRetryAt.After(now) || previous.Attempt >= MaxAttempts {
				return nil
			}
		}
		row := snap.Credential
		if row.CurrentVersionID == nil || *row.CurrentVersionID != target.InputVersionID || row.RetryBudget <= 0 ||
			(row.Status != "active" && row.Status != "expiring") || snap.Shop.Status == "disabled" || snap.Shop.DeletedAt.Valid {
			return ErrIneligible
		}
		attempt := 1
		if previous != nil {
			attempt = previous.Attempt + 1
		}
		row.RefreshGeneration++
		result = model.RefreshRun{TenantID: target.TenantID, CredentialID: target.CredentialID, InputVersionID: target.InputVersionID,
			Generation: row.RefreshGeneration, CandidateVersion: snap.Version.VersionNo + 1, Phase: PhaseIntent, Status: "running",
			Trigger: "manual", Attempt: attempt, StartedAt: now, UpdatedAt: now, TraceID: logger.TraceID(ctx)}
		if err := e.store.InsertRun(tx, &result); err != nil {
			return err
		}
		return e.store.SaveCredential(tx, row)
	})
	return result, err
}

func eligible(snap Snapshot, run model.RefreshRun) bool {
	c := snap.Credential
	return c.CurrentVersionID != nil && *c.CurrentVersionID == run.InputVersionID && c.RefreshGeneration == run.Generation &&
		c.Status != "disabled" && c.Status != "needs_reauth" && snap.Shop.Status != "disabled" && !snap.Shop.DeletedAt.Valid
}

func (e *Executor) withRun(ctx context.Context, target Target, id int64, fn func(context.Context, Snapshot, *model.RefreshRun) error) (model.RefreshRun, error) {
	var result model.RefreshRun
	if id <= 0 || target.TenantID <= 0 || target.CredentialID <= 0 {
		return result, ErrIneligible
	}
	err := e.store.Transaction(ctx, func(tx context.Context) error {
		snap, err := e.store.Lock(tx, target)
		if err != nil {
			return err
		}
		run, err := e.store.FindRun(tx, target, id)
		if err != nil {
			return err
		}
		if run == nil {
			return ErrIneligible
		}
		err = fn(tx, snap, run)
		result = *run
		return err
	})
	return result, err
}

// Execute claims intent exactly once. Redelivery of sending never sends again.
func (e *Executor) Execute(ctx context.Context, target Target, id int64) (model.RefreshRun, error) {
	var input []byte
	var snap Snapshot
	claimed := false
	run, err := e.withRun(ctx, target, id, func(tx context.Context, s Snapshot, r *model.RefreshRun) error {
		if r.Status != "running" || r.Phase != PhaseIntent {
			return nil
		}
		if !eligible(s, *r) {
			return e.supersede(tx, r)
		}
		var err error
		input, err = credential.OpenVersion(e.sealer, r.TenantID, s.Shop, s.Credential, s.Version)
		if err != nil {
			return err
		}
		now := e.now().UTC()
		deadline := now.Add(e.config.CallTimeout + e.config.FinishTimeout)
		r.Phase, r.SendDeadline, r.UpdatedAt = PhaseSending, &deadline, now
		s.Credential.Status = "refreshing"
		if err := e.store.SaveCredential(tx, s.Credential); err != nil {
			return err
		}
		if err := e.store.SaveRun(tx, *r); err != nil {
			return err
		}
		if err := e.store.Audit(tx, *r, "read", "success", now); err != nil {
			return err
		}
		claimed, snap = true, s
		return nil
	})
	defer clear(input)
	if err != nil {
		return run, err
	}
	if !claimed {
		if run.Phase == PhaseConsumed && run.Status == "running" {
			return e.Publish(ctx, target, id)
		}
		return run, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, e.config.CallTimeout)
	defer cancel()
	if callCtx.Err() != nil {
		return e.finishFailure(ctx, target, id, callCtx.Err())
	}
	result, callErr := e.platform.Refresh(callCtx, port.Request{TenantID: run.TenantID, CredentialID: run.CredentialID,
		InputVersionID: run.InputVersionID, Generation: run.Generation, Kind: snap.Credential.Kind, Values: input})
	defer clear(result.Values)
	if callErr != nil {
		return e.finishFailure(ctx, target, id, callErr)
	}
	return e.finishResult(ctx, target, run, snap, result)
}

func (e *Executor) supersede(ctx context.Context, run *model.RefreshRun) error {
	now := e.now().UTC()
	run.Status, run.Reason, run.FinishedAt, run.UpdatedAt = "skipped", "superseded", &now, now
	return e.store.SaveRun(ctx, *run)
}

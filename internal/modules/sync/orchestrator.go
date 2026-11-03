// Package sync implements bounded page orchestration with durable dispositions.
package sync

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
)

type RawSaver interface {
	SaveRaw(context.Context, int64, string, []ingestion.RawItem) ([]ingestion.RawRef, error)
}

// Dependencies keep network, locks and transaction ownership in bootstrap.
type Dependencies struct {
	Store  repo.EngineStore
	Replay repo.ReplayStore
	Raw    RawSaver
	Fetch  func(context.Context, repo.Work, string) (connector.FetchResult, error)
	Guard  func(context.Context, repo.Job, func(context.Context) error) error
}

type Orchestrator struct{ deps Dependencies }

func NewOrchestrator(deps Dependencies) (*Orchestrator, error) {
	if deps.Store == nil || deps.Raw == nil || deps.Fetch == nil || deps.Guard == nil || deps.Replay == nil {
		return nil, errors.New("sync orchestration dependencies are required")
	}
	return &Orchestrator{deps: deps}, nil
}

// StartRun persists the run and its dispatch atomically. No queue IO occurs here.
func (o *Orchestrator) StartRun(ctx context.Context, jobID int64, trigger string) (repo.Run, error) {
	return o.Start(ctx, repo.Start{JobID: jobID, Trigger: trigger})
}

func (o *Orchestrator) Start(ctx context.Context, start repo.Start) (repo.Run, error) {
	job, err := o.deps.Store.ResolveJob(ctx, start.JobID)
	if err != nil {
		return repo.Run{}, err
	}
	var run repo.Run
	err = o.deps.Guard(ctx, job, func(ctx context.Context) error {
		var err error
		run, err = o.deps.Store.CreateRun(ctx, start)
		return err
	})
	return run, err
}

// Execute processes a bounded slice of an existing chain. Event redelivery is
// fenced by sequence and cursor in CommitPage; a finished run is a safe no-op.
func (o *Orchestrator) Execute(ctx context.Context, jobID, runID int64) error {
	return o.ExecuteDispatch(ctx, jobID, runID, 0, -1)
}

// ExecuteDispatch verifies the durable dispatch generation under the resource lock.
func (o *Orchestrator) ExecuteDispatch(ctx context.Context, jobID, runID, tenant int64, dispatch int) error {
	job, err := o.deps.Store.ResolveJob(ctx, jobID)
	if err != nil {
		return err
	}
	return o.deps.Guard(ctx, job, func(ctx context.Context) error {
		work, err := o.deps.Store.LoadWork(ctx, jobID, runID)
		if err != nil {
			return err
		}
		if tenant != 0 && work.Job.TenantID != tenant {
			return errors.New("sync dispatch tenant mismatch")
		}
		if dispatch >= 0 && work.Run.Stats.Dispatch != dispatch {
			return nil
		}
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		return o.execute(ctx, jobID, runID)
	})
}

func (o *Orchestrator) execute(ctx context.Context, jobID, runID int64) error {
	start := time.Now()
	for pageNo := 1; ; pageNo++ {
		work, err := o.deps.Store.LoadWork(ctx, jobID, runID)
		if err != nil {
			return err
		}
		if !work.Run.Stats.Pending {
			return nil
		}
		pageCtx := scope.WithWorkAccess(ctx, work.Access)
		if work.Run.Stats.ReplayID > 0 {
			return o.replay(pageCtx, work)
		}
		cursor, err := o.deps.Store.LoadCursor(pageCtx)
		if err != nil {
			return err
		}
		result, err := o.deps.Fetch(pageCtx, work, cursor.Value)
		if err != nil {
			return o.deferFetch(pageCtx, work.Run, err)
		}
		if result.HasMore && (result.NextCursor == "" || result.NextCursor == cursor.Value) {
			return o.deps.Store.DeferRun(pageCtx, work.Run, "invalid_cursor", 0, true)
		}
		items, err := o.prepare(pageCtx, work, result.Items)
		if err != nil {
			return err
		}
		deadline, _ := ctx.Deadline()
		yield := pageNo >= work.Job.MaxPages || time.Since(start) >= 35*time.Second || time.Until(deadline) < 23*time.Second
		// Reserve time for the final short transaction inside the 60s budget.
		page := repo.Page{Run: work.Run, Before: cursor.Value, Next: result.NextCursor, Items: items, HasMore: result.HasMore, Yield: yield}
		_, err = o.deps.Store.CommitPage(pageCtx, page)
		if err != nil {
			return err
		}
		if !result.HasMore || yield {
			return nil
		}
	}
}

func (o *Orchestrator) prepare(ctx context.Context, work repo.Work, items []connector.FetchItem) ([]repo.Disposition, error) {
	raw := make([]ingestion.RawItem, len(items))
	for i, item := range items {
		raw[i] = ingestion.RawItem{ExternalID: item.ExternalID, Payload: item.Payload, FetchedAt: time.Now().UTC(), APIName: "fetch"}
	}
	refs, err := o.deps.Raw.SaveRaw(ctx, work.Job.ShopID, work.Job.Resource, raw)
	if err != nil {
		return nil, err
	}
	if len(refs) != len(items) {
		return nil, errors.New("incomplete raw dispositions")
	}
	result := make([]repo.Disposition, len(items))
	for i, item := range items {
		result[i] = interpret(work, refs[i], item.Payload)
	}
	return result, nil
}

func (o *Orchestrator) replay(ctx context.Context, work repo.Work) error {
	raw, ref, err := o.deps.Replay.LoadReplay(ctx, work.Run)
	if err != nil {
		return err
	}
	item := interpret(work, ref, raw)
	_, err = o.deps.Store.CommitPage(ctx, repo.Page{Run: work.Run, Items: []repo.Disposition{item}})
	return err
}

func (o *Orchestrator) deferFetch(ctx context.Context, run repo.Run, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var platform *connector.PlatformError
	delay, reason, terminal := time.Second, "fetch_unavailable", false
	if errors.As(err, &platform) {
		switch platform.Kind {
		case connector.ErrAuthExpired:
			reason, terminal = "credential_expired", true
		case connector.ErrInvalidRequest:
			reason, terminal = "fetch_invalid", true
		case connector.ErrRateLimited:
			reason = "rate_limited"
			delay = max(time.Second, time.Duration(platform.RetryAfterSeconds)*time.Second)
		}
	}
	return o.deps.Store.DeferRun(ctx, run, reason, delay, terminal)
}

// Package sync implements bounded page orchestration with durable dispositions.
package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Store    repo.EngineStore
	Replay   repo.ReplayStore
	Raw      RawSaver
	Handlers map[string]ResourceHandler
	Fetch    func(context.Context, repo.Work, string) (connector.FetchResult, error)
	Guard    func(context.Context, repo.Job, func(context.Context) error) error
	Slice    func(context.Context, repo.Job, repo.Run, func(context.Context) error) error
	Order    func(context.Context, repo.Work) error
}

type Orchestrator struct{ deps Dependencies }

func NewOrchestrator(deps Dependencies) (*Orchestrator, error) {
	if deps.Store == nil || deps.Raw == nil || deps.Fetch == nil || deps.Guard == nil || deps.Replay == nil || len(deps.Handlers) == 0 {
		return nil, errors.New("sync orchestration dependencies are required")
	}
	deps.Handlers = cloneResourceHandlers(deps.Handlers)
	return &Orchestrator{deps: deps}, nil
}

// StartRun persists the run and its dispatch atomically. No queue IO occurs here.
func (o *Orchestrator) StartRun(ctx context.Context, jobID int64, trigger string) (repo.Run, error) {
	return o.Start(ctx, repo.Start{JobID: jobID, Trigger: trigger})
}

func (o *Orchestrator) Start(ctx context.Context, start repo.Start) (repo.Run, error) {
	return o.deps.Store.CreateRun(ctx, start)
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
	work, err := o.deps.Store.LoadWork(ctx, jobID, runID)
	if err != nil {
		return err
	}
	if tenant != 0 && work.Job.TenantID != tenant {
		return errors.New("sync dispatch tenant mismatch")
	}
	if !work.Run.Stats.Pending || (dispatch >= 0 && work.Run.Stats.Dispatch != dispatch) {
		return nil
	}
	execute := func(ctx context.Context) error {
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
	if o.deps.Slice != nil {
		return o.deps.Slice(ctx, job, work.Run, execute)
	}
	return execute(ctx)
}

func (o *Orchestrator) execute(ctx context.Context, jobID, runID int64) error {
	work, err := o.deps.Store.LoadWork(ctx, jobID, runID)
	if err != nil {
		return err
	}
	if work.Job.Resource == string(connector.ResourceOrder) && work.Run.Stats.ReplayID == 0 {
		if o.deps.Order == nil {
			return errors.New("order acquisition port is required")
		}
		return o.deps.Order(scope.WithWorkAccess(ctx, work.Access), work)
	}
	start := time.Now()
	for pageNo := 1; ; pageNo++ {
		work, err := o.deps.Store.LoadWork(ctx, jobID, runID)
		if err != nil {
			return err
		}
		if !work.Run.Stats.Pending {
			return nil
		}
		if _, err := o.resourceHandler(work.Job.Resource); err != nil {
			return err
		}
		pageCtx := scope.WithWorkAccess(ctx, work.Access)
		if work.Run.Stats.ReplayID > 0 {
			return o.replay(pageCtx, work)
		}
		cursor, err := o.deps.Store.LoadCursor(pageCtx)
		if err != nil {
			return err
		}
		fetchCursor := cursor.Value
		// A new page-mode cycle starts at its first page. Keep the previous
		// checkpoint intact until that page commits; continuations retain theirs.
		if work.Run.Stats.Fetched == 0 {
			fetchCursor = ""
		}
		result, err := o.deps.Fetch(pageCtx, work, fetchCursor)
		if err != nil {
			return o.deferFetch(pageCtx, work.Run, err)
		}
		if result.HasMore && (result.NextCursor == "" || result.NextCursor == fetchCursor) {
			return o.deps.Store.DeferRun(pageCtx, work.Run, "invalid_cursor", 0, true)
		}
		items, err := o.prepare(pageCtx, work, result.Items)
		if err != nil {
			return err
		}
		deadline, _ := ctx.Deadline()
		yield := pageNo >= work.Job.MaxPages || time.Since(start) >= 35*time.Second || time.Until(deadline) < 23*time.Second
		// Reserve time for the final short transaction inside the 60s budget.
		page := repo.Page{Run: work.Run, Before: cursor.Value, Next: result.NextCursor, Items: items, HasMore: result.HasMore, Yield: yield, Required: work.Mapping.RequiredPaths()}
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
		fields, err := ingestion.CollectStructure(item.Payload)
		if err != nil {
			return nil, err
		}
		interpreted, err := o.interpret(work, refs[i], item.Payload)
		if err != nil {
			return nil, err
		}
		result[i] = interpreted
		result[i].Structure = fields
		result[i].MissingRequired, err = work.Mapping.MissingRequiredPaths(item.Payload)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (o *Orchestrator) replay(ctx context.Context, work repo.Work) error {
	raw, ref, err := o.deps.Replay.LoadReplay(ctx, work.Run)
	if err != nil {
		return err
	}
	item, err := o.interpret(work, ref, raw)
	if err != nil {
		return err
	}
	_, err = o.deps.Store.CommitPage(ctx, repo.Page{Run: work.Run, Items: []repo.Disposition{item}})
	return err
}

func (o *Orchestrator) interpret(work repo.Work, ref ingestion.RawRef, raw json.RawMessage) (repo.Disposition, error) {
	handler, err := o.resourceHandler(work.Job.Resource)
	if err != nil {
		return repo.Disposition{}, err
	}
	return interpret(work, ref, raw, handler), nil
}

func (o *Orchestrator) resourceHandler(resource string) (ResourceHandler, error) {
	return lookupResourceHandler(o.deps.Handlers, resource)
}

func lookupResourceHandler(handlers map[string]ResourceHandler, resource string) (ResourceHandler, error) {
	handler, ok := handlers[resource]
	if !ok || handler == nil {
		return nil, fmt.Errorf("sync resource %q is not registered", resource)
	}
	return handler, nil
}

func cloneResourceHandlers(source map[string]ResourceHandler) map[string]ResourceHandler {
	result := make(map[string]ResourceHandler, len(source))
	for resource, handler := range source {
		result[resource] = handler
	}
	return result
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

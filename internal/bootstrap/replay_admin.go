package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *syncEngineStore) replayAdmin(ctx context.Context, code string) (context.Context, error) {
	access, err := identityscope.From(ctx)
	if err != nil {
		return ctx, syncDenied()
	}
	return s.Authorize(ctx, access.Tenant, access.User, code)
}

// ListReplays returns a live shop-scoped page and exact count.
func (s *syncEngineStore) ListReplays(ctx context.Context, page repo.PageQuery) (repo.Result, error) {
	ctx, err := s.replayAdmin(ctx, permission.SyncReplayList)
	if err != nil {
		return repo.Result{}, err
	}
	page.Number = max(1, page.Number)
	if page.Size <= 0 {
		page.Size = 20
	}
	page.Size = min(100, page.Size)
	q, cancel, err := s.humanQuery(ctx, "replay_run", "Replay.List")
	if err != nil {
		return repo.Result{}, err
	}
	defer cancel()
	if page.ShopID > 0 {
		q = q.Where("x.shop_id=?", page.ShopID)
	}
	if page.Status != "" {
		q = q.Where("x.status=?", page.Status)
	}
	result := repo.Result{Page: page.Number, PageSize: page.Size}
	if err := q.Session(&gorm.Session{}).Count(&result.Total).Error; err != nil {
		return result, err
	}
	var rows []syncmodule.ReplayRun
	if err := q.Select(replayProjection()).Order("x.started_at DESC,x.id DESC").Limit(page.Size).Offset((page.Number - 1) * page.Size).Find(&rows).Error; err != nil {
		return result, err
	}
	for i := range rows {
		if err := s.replayProgress(ctx, &rows[i]); err != nil {
			return result, err
		}
	}
	result.Items = rows
	return result, nil
}

func replayProjection() string {
	return "x.id,x.tenant_id,x.platform_id,x.shop_id,x.resource_type,x.mapping_version_id,x.mapping_checksum,x.mapping_spec,x.function_version,x.authorization_generation,x.scheduling_generation,x.from_raw_id,x.to_raw_id,x.total,x.processed,x.checkpoint_key,x.upper_key,x.stats,x.status,x.triggered_by,x.started_at,x.finished_at"
}

// CancelReplay fences in-flight work atomically with terminal status and pin release.
func (s *syncEngineStore) CancelReplay(ctx context.Context, id int64, op *idem.Operation) (syncmodule.ReplayRun, error) {
	var run syncmodule.ReplayRun
	ctx, err := s.replayAdmin(ctx, permission.SyncReplayCancel)
	if err != nil {
		return run, err
	}
	err = s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel, err := s.humanQuery(ctx, "replay_run", "Replay.Cancel")
		if err != nil {
			return err
		}
		defer cancel()
		if err := q.Select(replayProjection()).Where("x.id=?", id).Take(&run).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return syncMissing()
			}
			return err
		}
		raw, stop := s.database.WithContext(ctx)
		defer stop()
		grant, err := lockReplayForCancel(raw, &run)
		if err != nil {
			return err
		}
		if op != nil {
			access, err := identityscope.From(ctx)
			if err != nil || op.TenantID != run.TenantID || op.UserID != access.User {
				return syncDenied()
			}
			claim, err := newIdempotencyStore(s.database).Claim(ctx, *op)
			if err != nil {
				return err
			}
			if claim.Action == idem.Replay {
				return json.Unmarshal(claim.Result.Body, &run)
			}
		}
		if err := s.cancelReplayLocked(raw, &run, grant); err != nil {
			return err
		}
		if op == nil {
			return nil
		}
		result, err := idem.JSONResult(200, run)
		if err != nil {
			return err
		}
		_, err = newIdempotencyStore(s.database).Complete(ctx, *op, result)
		return err
	})
	return run, err
}

func lockReplayForCancel(q *gorm.DB, run *syncmodule.ReplayRun) (schedulingGrant, error) {
	var job int64
	if err := q.Table("sync_job").Select("id").Where("tenant_id=? AND shop_id=? AND resource_type=?", run.TenantID, run.ShopID, run.ResourceType).Take(&job).Error; err != nil {
		return schedulingGrant{}, err
	}
	grant, err := lockSchedule(q, job)
	if err != nil {
		return grant, err
	}
	if err := q.Table("replay_run").Select(replayColumns).Where("tenant_id=? AND id=?", run.TenantID, run.ID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(run).Error; err != nil {
		return grant, err
	}
	return grant, nil
}

func (s *syncEngineStore) cancelReplayLocked(q *gorm.DB, run *syncmodule.ReplayRun, grant schedulingGrant) error {
	if run.FinishedAt != nil {
		return nil
	}
	now := s.now().UTC()
	run.Status = "cancelled"
	run.FinishedAt = &now
	run.SchedulingGeneration++
	if grant.Kind == "replay" && grant.RunID == run.ID {
		grant.Epoch++
		grant.Owner = ""
		if err := saveSchedule(q, grant); err != nil {
			return err
		}
	}
	if err := q.Table("replay_run").Where("tenant_id=? AND id=?", run.TenantID, run.ID).Update("scheduling_generation", run.SchedulingGeneration).Error; err != nil {
		return err
	}
	if err := saveReplay(q, *run); err != nil {
		return err
	}
	return releaseReplayPin(q, run.ID)
}

func (s *syncEngineStore) replayProgress(ctx context.Context, run *syncmodule.ReplayRun) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	now := s.now().UTC()
	start := now.Add(-15 * time.Minute)
	if run.StartedAt.After(start) {
		start = run.StartedAt
	}
	var sample struct {
		Count   int64
		Batches int64
		Last    *time.Time
	}
	if err := q.Raw(`SELECT count(*) AS count,count(DISTINCT disposed_at) AS batches,max(disposed_at) AS last FROM replay_run_item WHERE run_id=? AND disposed_at>=? AND disposed_at<=?`, run.ID, start, now).Scan(&sample).Error; err != nil {
		return err
	}
	run.LastCommittedAt = sample.Last
	if run.FinishedAt != nil {
		if run.Processed == run.Total {
			zero := int64(0)
			run.ETASeconds = &zero
		}
		return nil
	}
	elapsed := now.Sub(start).Seconds()
	if sample.Batches < 3 || elapsed < 60 || sample.Last == nil || now.Sub(*sample.Last) > 5*time.Minute {
		return nil
	}
	eta := int64(math.Ceil(float64(run.Total-run.Processed) * elapsed / float64(sample.Count)))
	run.ETASeconds = &eta
	return nil
}

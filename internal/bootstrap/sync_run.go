package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *syncEngineStore) CreateRun(ctx context.Context, start repo.Start) (repo.Run, error) {
	if err := engineQuery("SyncEngine.CreateRun"); err != nil {
		return repo.Run{}, err
	}
	if start.Trigger != "manual" && start.Trigger != "cron" && start.Trigger != "retry" {
		return repo.Run{}, syncDenied()
	}
	var run repo.Run
	err := s.database.Transaction(ctx, func(ctx context.Context) error {
		job, err := s.ResolveJob(ctx, start.JobID)
		if err != nil {
			return err
		}
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		var lockedID int64
		if err := q.Table("sync_job").Select("id").Where("tenant_id=? AND id=? AND enabled=true", job.TenantID, job.ID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&lockedID).Error; err != nil {
			return err
		}
		actor, err := s.startActor(ctx, job, start)
		if err != nil {
			return err
		}
		if start.Operation != nil {
			if actor == nil || start.Operation.TenantID != job.TenantID || start.Operation.UserID != *actor {
				return syncDenied()
			}
			claim, err := newIdempotencyStore(s.database).Claim(ctx, *start.Operation)
			if err != nil {
				return err
			}
			if claim.Action == idem.Replay {
				return json.Unmarshal(claim.Result.Body, &run)
			}
		}
		if start.Submission != "" {
			var previous []repo.Run
			err := q.Table("sync_run").Select(syncRunColumns).Where("tenant_id=? AND job_id=? AND stats->>'submission'=?", job.TenantID, job.ID, start.Submission).Limit(1).Find(&previous).Error
			if err != nil {
				return err
			}
			if len(previous) > 0 {
				run = previous[0]
				return nil
			}
		}
		var count int64
		if err := q.Table("sync_run").Where("tenant_id=? AND shop_id=? AND resource_type=? AND (status='running' OR stats->>'pending'='true')", job.TenantID, job.ShopID, job.Resource).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return syncBusy()
		}
		stats, err := s.startStats(ctx, job, start)
		if err != nil {
			return err
		}
		run = repo.Run{TenantID: job.TenantID, JobID: job.ID, ShopID: job.ShopID, ResourceType: job.Resource, Trigger: start.Trigger,
			Status: "running", Stats: stats, TriggeredBy: actor, StartedAt: time.Now().UTC(), TraceID: logger.TraceID(ctx)}
		if err := q.Table("sync_run").Create(&run).Error; err != nil {
			return err
		}
		if err := q.Table("sync_job").Where("id=? AND tenant_id=?", job.ID, job.TenantID).Updates(map[string]any{"last_run_id": run.ID, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		if err := s.emitRun(ctx, run, 0); err != nil {
			return err
		}
		if start.Operation == nil {
			return nil
		}
		result, err := idem.JSONResult(201, run)
		if err != nil {
			return err
		}
		_, err = newIdempotencyStore(s.database).Complete(ctx, *start.Operation, result)
		return err
	})
	return run, err
}

func (s *syncEngineStore) startActor(ctx context.Context, job repo.Job, start repo.Start) (*int64, error) {
	if start.Trigger == "cron" {
		return nil, nil
	}
	if err := s.checkHuman(ctx, job, runPermission(start.ReplayKind)); err != nil {
		return nil, err
	}
	human, err := identityscope.From(ctx)
	if err != nil {
		return nil, err
	}
	return &human.User, nil
}

func (s *syncEngineStore) startStats(ctx context.Context, job repo.Job, start repo.Start) (repo.Stats, error) {
	stats := repo.Stats{MappingVersion: job.MappingVersion, Generation: job.Generation, Pending: true, Submission: start.Submission, ReplayKind: start.ReplayKind, ReplayID: start.ReplayID}
	if start.ReplayID > 0 {
		source, err := s.replayMetadata(ctx, job, start.ReplayKind, start.ReplayID)
		if err != nil {
			return stats, err
		}
		stats.MappingVersion = source.MappingVersion
	}
	return stats, nil
}

func (s *syncEngineStore) emitRun(ctx context.Context, run repo.Run, delay time.Duration) error {
	payload, err := json.Marshal(struct {
		JobID    int64 `json:"job_id"`
		RunID    int64 `json:"run_id"`
		Sequence int   `json:"sequence"`
	}{run.JobID, run.ID, run.Stats.Dispatch})
	if err != nil {
		return err
	}
	return outbox.Emit(ctx, s.database, outbox.Event{TenantID: run.TenantID, AggregateType: "sync_run", AggregateID: strconv.FormatInt(run.ID, 10), EventType: "sync.dispatch", Payload: payload, AvailableAt: time.Now().UTC().Add(delay)})
}

func (s *syncEngineStore) LoadCursor(ctx context.Context) (repo.Cursor, error) {
	if err := engineQuery("SyncEngine.LoadCursor"); err != nil {
		return repo.Cursor{}, err
	}
	a, err := s.capability(ctx, "sync.cursor")
	if err != nil {
		return repo.Cursor{}, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row struct {
		CursorValue string
		UpdatedAt   time.Time
	}
	err = q.Table("sync_cursor").Select("cursor_value,updated_at").Where("tenant_id=? AND shop_id=? AND resource_type=?", a.TenantID(), a.ShopID(), a.Resource()).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return repo.Cursor{}, nil
	}
	return repo.Cursor{Value: row.CursorValue, UpdatedAt: row.UpdatedAt}, err
}

func (s *syncEngineStore) DeferRun(ctx context.Context, run repo.Run, code string, delay time.Duration, terminal bool) error {
	if err := engineQuery("SyncEngine.DeferRun"); err != nil {
		return err
	}
	a, err := s.capability(ctx, "sync.defer")
	if err != nil {
		return err
	}
	if a.RunID() != run.ID || a.JobID() != run.JobID || a.TenantID() != run.TenantID {
		return syncDenied()
	}
	return s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		var current repo.Run
		if err := q.Table("sync_run").Select(syncRunColumns).Where("id=? AND tenant_id=? AND job_id=?", a.RunID(), a.TenantID(), a.JobID()).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&current).Error; err != nil {
			return err
		}
		if current.Stats.Sequence != run.Stats.Sequence || !current.Stats.Pending {
			return nil
		}
		current.Stats.Retries++
		current.Stats.CycleFailed = true
		terminal = terminal || current.Stats.Retries >= 3
		current.Stats.Sequence++
		current.Stats.Dispatch++
		current.Stats.Pending = !terminal
		current.Status = "partial"
		if terminal {
			current.Status = "failed"
			now := time.Now().UTC()
			current.FinishedAt = &now
		}
		current.ErrorCode, current.ErrorMsg = code, "page fetch did not complete"
		if err := saveSyncRun(q, current); err != nil {
			return err
		}
		if err := q.Table("sync_error").Create(map[string]any{"run_id": current.ID, "stage": "fetch", "error_code": code, "message": "page fetch did not complete", "retryable": !terminal}).Error; err != nil {
			return err
		}
		if code == "credential_expired" {
			return q.Table("sync_job").Where("id=? AND tenant_id=?", a.JobID(), a.TenantID()).Updates(map[string]any{"enabled": false, "updated_at": time.Now().UTC()}).Error
		}
		if terminal {
			return nil
		}
		delay = max(delay, time.Second*time.Duration(1<<min(current.Stats.Retries-1, 8)))
		return s.emitRun(ctx, current, delay)
	})
}

func saveSyncRun(q *gorm.DB, run repo.Run) error {
	raw, err := json.Marshal(run.Stats)
	if err != nil {
		return err
	}
	return syncChanged(q.Table("sync_run").Where("id=? AND tenant_id=? AND job_id=?", run.ID, run.TenantID, run.JobID).
		Updates(map[string]any{"status": run.Status, "stats": json.RawMessage(raw), "finished_at": run.FinishedAt, "error_code": run.ErrorCode, "error_msg": run.ErrorMsg}))
}

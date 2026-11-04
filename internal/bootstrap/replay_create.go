package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
)

const replayColumns = "id,tenant_id,platform_id,shop_id,resource_type,mapping_version_id,mapping_checksum,mapping_spec,function_version,authorization_generation,scheduling_generation,from_raw_id,to_raw_id,total,processed,checkpoint_key,upper_key,stats,status,triggered_by,started_at,finished_at"

func replayInvalid() error { return errs.New(60601, 400, "重放范围非法或不在线") }

// CreateReplay freezes membership and the mapping release in a single MVCC view.
func (s *syncEngineStore) CreateReplay(ctx context.Context, request syncmodule.ReplayRequest, op *idem.Operation) (syncmodule.ReplayRun, error) {
	var run syncmodule.ReplayRun
	if err := engineQuery("Replay.Create"); err != nil {
		return run, err
	}
	if err := validateReplayScope(request); err != nil {
		return run, err
	}
	err := s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		if err := q.Exec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ").Error; err != nil {
			return err
		}
		job, actor, err := s.replayCreationAccess(ctx, q, request)
		if err != nil {
			return err
		}
		if op != nil {
			if op.TenantID != job.TenantID || op.UserID != actor {
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
		run = syncmodule.ReplayRun{TenantID: job.TenantID, PlatformID: job.PlatformID, ShopID: job.ShopID, ResourceType: job.Resource,
			MappingVersionID: request.MappingVersionID, AuthorizationGeneration: job.Generation, TriggeredBy: actor,
			Status: "running", StartedAt: time.Now().UTC(), CheckpointKey: json.RawMessage(`{}`), UpperKey: json.RawMessage(`{}`)}
		if err := s.freezeReplayMapping(q, &run); err != nil {
			return err
		}
		if err := q.Table("replay_run").Create(&run).Error; err != nil {
			return err
		}
		if err := freezeReplayMembers(q, &run, request.Scope); err != nil {
			return err
		}
		if run.Total == 0 {
			run.Status = "success"
			run.FinishedAt = &run.StartedAt
		}
		if err := saveReplay(q, run); err != nil {
			return err
		}
		if run.Total > 0 {
			if err := s.emitReplay(ctx, run, 0); err != nil {
				return err
			}
		}
		if op == nil {
			return nil
		}
		result, err := idem.JSONResult(201, run)
		if err != nil {
			return err
		}
		_, err = newIdempotencyStore(s.database).Complete(ctx, *op, result)
		return err
	})
	return run, err
}

func validateReplayScope(r syncmodule.ReplayRequest) error {
	if r.ShopID <= 0 || r.MappingVersionID <= 0 || r.Resource == "" {
		return replayInvalid()
	}
	s := r.Scope
	switch s.Type {
	case "time":
		if s.From == nil || s.To == nil || s.From.IsZero() || !s.From.Before(*s.To) || len(s.IDs) > 0 || s.FromID != 0 || s.ToID != 0 {
			return replayInvalid()
		}
	case "ids":
		if s.From != nil || s.To != nil {
			return replayInvalid()
		}
		if len(s.IDs) > 0 {
			if len(s.IDs) > 500000 || s.FromID != 0 || s.ToID != 0 {
				return replayInvalid()
			}
			for _, id := range s.IDs {
				if id <= 0 {
					return replayInvalid()
				}
			}
		} else if s.FromID <= 0 || s.ToID < s.FromID {
			return replayInvalid()
		}
	case "quarantine":
		if s.From != nil || s.To != nil || len(s.IDs) > 0 || s.FromID != 0 || s.ToID != 0 {
			return replayInvalid()
		}
	default:
		return replayInvalid()
	}
	return nil
}

func (s *syncEngineStore) replayCreationAccess(ctx context.Context, q *gorm.DB, r syncmodule.ReplayRequest) (repo.Job, int64, error) {
	access, err := identityscope.From(ctx)
	if err != nil {
		return repo.Job{}, 0, syncDenied()
	}
	var id int64
	if err := q.Table("sync_job").Select("id").Where("tenant_id=? AND shop_id=? AND resource_type=? AND enabled=true", access.Tenant, r.ShopID, r.Resource).Take(&id).Error; err != nil {
		return repo.Job{}, 0, replayInvalid()
	}
	job, err := s.ResolveJob(ctx, id)
	if err != nil {
		return job, 0, err
	}
	if err := s.checkHuman(ctx, job, permission.SyncReplayRun); err != nil {
		return job, 0, err
	}
	return job, access.User, nil
}

func (s *syncEngineStore) freezeReplayMapping(q *gorm.DB, run *syncmodule.ReplayRun) error {
	var row struct {
		Spec     json.RawMessage
		Checksum string
	}
	err := q.Table("mapping_version v").Select("v.spec,v.checksum").Joins("JOIN mapping_profile p ON p.tenant_id=v.tenant_id AND p.id=v.profile_id").
		Where("v.tenant_id=? AND v.id=? AND v.status='published' AND p.platform_id=? AND p.resource_type=?", run.TenantID, run.MappingVersionID, run.PlatformID, run.ResourceType).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return replayInvalid()
	}
	if err != nil {
		return err
	}
	program, err := s.compiled.GetOrCompile(run.MappingVersionID, row.Checksum, row.Spec)
	if err != nil {
		return err
	}
	run.MappingChecksum, run.MappingSpec, run.FunctionVersion = program.Checksum, row.Spec, ingestion.FunctionSetVersion
	return nil
}

// ReadReplay returns progress only after rechecking current shop scope.
func (s *syncEngineStore) ReadReplay(ctx context.Context, id int64) (syncmodule.ReplayRun, error) {
	var run syncmodule.ReplayRun
	q, cancel, err := s.humanQuery(ctx, "replay_run", "Replay.Read")
	if err != nil {
		return run, err
	}
	defer cancel()
	err = q.Select("x.id,x.tenant_id,x.platform_id,x.shop_id,x.resource_type,x.mapping_version_id,x.mapping_checksum,x.mapping_spec,x.function_version,x.authorization_generation,x.scheduling_generation,x.from_raw_id,x.to_raw_id,x.total,x.processed,x.checkpoint_key,x.upper_key,x.stats,x.status,x.triggered_by,x.started_at,x.finished_at").Where("x.id=?", id).Take(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return run, syncMissing()
	}
	return run, err
}

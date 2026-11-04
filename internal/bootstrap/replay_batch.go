package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProcessReplay commits one bounded batch, including its durable continuation.
func (s *syncEngineStore) ProcessReplay(ctx context.Context, tenant, id, processed int64) (syncmodule.ReplayRun, error) {
	var run syncmodule.ReplayRun
	if err := engineQuery("Replay.Batch"); err != nil {
		return run, err
	}
	err := s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		job, err := s.lockReplay(ctx, q, tenant, id, &run)
		if err != nil {
			return err
		}
		if run.FinishedAt != nil || run.Processed != processed {
			return nil
		}
		if err := s.authorizeReplay(ctx, job, run); err != nil {
			return err
		}
		if run.FunctionVersion != ingestion.FunctionSetVersion {
			return errors.New("replay function version unavailable")
		}
		mapping, err := s.compiled.GetOrCompile(run.MappingVersionID, run.MappingChecksum, run.MappingSpec)
		if err != nil {
			return err
		}
		members, err := nextReplayMembers(q, run)
		if err != nil {
			return err
		}
		for _, member := range members {
			if err := s.applyReplayMember(ctx, q, &run, job, mapping, member); err != nil {
				return err
			}
		}
		run.Processed = run.Stats.Processed()
		if run.Processed == run.Total {
			now := time.Now().UTC()
			run.FinishedAt = &now
			run.Status = "success"
			if run.Stats.Failed+run.Stats.Quarantined > 0 {
				run.Status = "partial"
			}
		}
		if err := saveReplay(q, run); err != nil {
			return err
		}
		if run.FinishedAt == nil {
			return s.emitReplay(ctx, run, run.Processed)
		}
		return nil
	})
	return run, err
}

func (s *syncEngineStore) lockReplay(ctx context.Context, q *gorm.DB, tenant, id int64, run *syncmodule.ReplayRun) (repo.Job, error) {
	var identity struct {
		ShopID       int64
		ResourceType string
	}
	if err := q.Table("replay_run").Select("shop_id,resource_type").Where("tenant_id=? AND id=?", tenant, id).Take(&identity).Error; err != nil {
		return repo.Job{}, err
	}
	var jobID int64
	if err := q.Table("sync_job").Select("id").Where("tenant_id=? AND shop_id=? AND resource_type=?", tenant, identity.ShopID, identity.ResourceType).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&jobID).Error; err != nil {
		return repo.Job{}, err
	}
	if err := q.Table("replay_run").Select(replayColumns).Where("tenant_id=? AND id=?", tenant, id).Clauses(clause.Locking{Strength: "UPDATE"}).Take(run).Error; err != nil {
		return repo.Job{}, err
	}
	return s.ResolveJob(ctx, jobID)
}

func (s *syncEngineStore) authorizeReplay(ctx context.Context, job repo.Job, run syncmodule.ReplayRun) error {
	if job.Generation != run.AuthorizationGeneration {
		return errRunAuthorizationRevoked
	}
	human, err := s.Authorize(ctx, run.TenantID, run.TriggeredBy, permission.SyncReplayRun)
	if err != nil {
		return err
	}
	return s.checkHuman(human, job, permission.SyncReplayRun)
}

func nextReplayMembers(q *gorm.DB, r syncmodule.ReplayRun) ([]replayMember, error) {
	var key syncmodule.ReplayKey
	if err := json.Unmarshal(r.CheckpointKey, &key); err != nil {
		return nil, err
	}
	query := q.Table("replay_run_item m").Select("m.id,m.fetched_at,m.external_id,m.payload_sha256,m.outcome").Where("m.run_id=?", r.ID)
	if key.ID > 0 {
		query = query.Where("(m.fetched_at,m.id)>(?,?)", key.FetchedAt, key.ID)
	}
	var rows []replayMember
	if err := query.Order("m.fetched_at,m.id").Limit(200).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 && r.Processed < r.Total {
		return nil, errors.New("replay members unavailable; checkpoint retained")
	}
	for i := range rows {
		if rows[i].Outcome != "" {
			return nil, errors.New("replay member already disposed beyond checkpoint")
		}
		var raw struct{ Payload json.RawMessage }
		err := q.Table("raw_payload").Select("payload").Where("tenant_id=? AND platform_id=? AND shop_id=? AND resource_type=? AND fetched_at=? AND id=? AND external_id=? AND payload_sha256=?", r.TenantID, r.PlatformID, r.ShopID, r.ResourceType, rows[i].FetchedAt, rows[i].ID, rows[i].ExternalID, rows[i].PayloadSHA256).Take(&raw).Error
		if err != nil {
			logger.From(q.Statement.Context).ErrorContext(q.Statement.Context, "replay raw unavailable; checkpoint retained", "module", "sync", "action", "replay_raw_unavailable", "run_id", r.ID, "raw_id", rows[i].ID)
			return nil, errors.New("replay raw unavailable; checkpoint retained")
		}
		rows[i].Payload = raw.Payload
	}
	return rows, nil
}

func (s *syncEngineStore) applyReplayMember(ctx context.Context, q *gorm.DB, r *syncmodule.ReplayRun, job repo.Job, mapping *ingestion.Compiled, m replayMember) error {
	handler := s.handlers[r.ResourceType]
	if handler == nil {
		return errors.New("replay resource handler unavailable")
	}
	ref := ingestion.RawRef{ID: m.ID, FetchedAt: m.FetchedAt, ExternalID: m.ExternalID}
	item := syncmodule.InterpretReplay(r.TenantID, r.PlatformID, r.ShopID, mapping, ref, m.Payload, handler)
	outcome := ""
	if item.Reason == "" {
		stats, err := handler.Upsert(ctx, q, item.Mapped)
		if err != nil {
			return err
		}
		item.Reason = stats.Reason
		if stats.Quarantined > 0 && item.Reason == "" {
			return errors.New("uncommitted replay canonical disposition")
		}
		switch {
		case stats.Inserted == 1:
			outcome = "inserted"
			r.Stats.Inserted++
		case stats.Updated == 1:
			outcome = "updated"
			r.Stats.Updated++
		case stats.SkippedStale == 1:
			outcome = "skipped_stale"
			r.Stats.SkippedStale++
		case stats.SkippedSame == 1:
			outcome = "skipped_same"
			r.Stats.SkippedSame++
		}
	}
	if item.Reason != "" {
		if err := persistReplayDisposition(q, *r, job, item); err != nil {
			return err
		}
		outcome = "quarantined"
		r.Stats.Quarantined++
		if item.Dead {
			outcome = "failed"
			r.Stats.Quarantined--
			r.Stats.Failed++
		}
	}
	if outcome == "" {
		return errors.New("replay handler returned no disposition")
	}
	if err := syncChanged(q.Table("replay_run_item").Where("run_id=? AND fetched_at=? AND id=? AND outcome IS NULL", r.ID, m.FetchedAt, m.ID).Updates(map[string]any{"outcome": outcome, "reason_code": item.Reason, "disposed_at": time.Now().UTC()})); err != nil {
		return err
	}
	key, err := json.Marshal(syncmodule.ReplayKey{FetchedAt: m.FetchedAt.UTC(), ID: m.ID})
	r.CheckpointKey = key
	return err
}

func persistReplayDisposition(q *gorm.DB, r syncmodule.ReplayRun, job repo.Job, item repo.Disposition) error {
	metadata, err := json.Marshal(repo.ReplayRaw{RawID: item.Raw.ID, FetchedAt: item.Raw.FetchedAt, MappingVersion: r.MappingVersionID, JobID: job.ID, RunID: r.ID})
	if err != nil {
		return err
	}
	row := map[string]any{"tenant_id": r.TenantID, "shop_id": r.ShopID, "resource_type": r.ResourceType, "external_id": item.Raw.ExternalID}
	if item.Dead {
		row["reason"], row["payload"] = item.Reason, json.RawMessage(metadata)
		return q.Table("dead_letter").Create(row).Error
	}
	switch item.Reason {
	case "missing_required", "type_mismatch", "invalid_value", "source_metadata_unknown", "source_identity_ambiguous":
	default:
		return errors.New("invalid replay quarantine reason")
	}
	row["raw_id"], row["reason_code"], row["reason_detail"] = item.Raw.ID, item.Reason, json.RawMessage(metadata)
	return q.Table("quarantine_record").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "raw_id"}, {Name: "reason_code"}}, DoNothing: true}).Create(row).Error
}

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
	return s.processReplayWithGuard(ctx, tenant, id, processed, nil)
}

func (s *syncEngineStore) processReplayWithGuard(ctx context.Context, tenant, id, processed int64, guard func(context.Context, repo.Job, func(context.Context) error) error) (syncmodule.ReplayRun, error) {
	q, cancel := s.database.WithContext(ctx)
	var run syncmodule.ReplayRun
	err := q.Table("replay_run").Select(replayColumns).Where("tenant_id=? AND id=?", tenant, id).Take(&run).Error
	var jobID int64
	if err == nil && run.FinishedAt == nil && run.Processed == processed {
		err = q.Table("sync_job").Select("id").Where("tenant_id=? AND shop_id=? AND resource_type=?", tenant, run.ShopID, run.ResourceType).Take(&jobID).Error
	}
	cancel()
	if err != nil || run.FinishedAt != nil || run.Processed != processed {
		return run, err
	}
	grant, err := s.acquireGrant(ctx, jobID, "replay", id)
	if errors.Is(err, errGrantBusy) {
		return run, s.emitReplayAfter(ctx, run, processed, 5*time.Second)
	}
	if err != nil {
		return run, err
	}
	work, stop := context.WithTimeout(context.WithValue(ctx, grantKey{}, grant), grant.Deadline.Sub(s.now()))
	apply := func(ctx context.Context) error {
		var err error
		run, err = s.processGrantedReplay(ctx, tenant, id, processed)
		return err
	}
	if guard == nil {
		err = apply(work)
	} else {
		err = guard(work, repo.Job{ID: jobID, TenantID: tenant, ShopID: run.ShopID, Resource: run.ResourceType}, apply)
	}
	stop()
	cleanup, finish := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer finish()
	return run, errors.Join(err, s.releaseGrant(cleanup, grant))
}

func (s *syncEngineStore) processGrantedReplay(ctx context.Context, tenant, id, processed int64) (syncmodule.ReplayRun, error) {
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
		if err := s.fenceGrant(ctx, q, "replay", run.ID, run.SchedulingGeneration); err != nil {
			return err
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
		if err := s.applyReplayMembers(ctx, q, &run, job, mapping, members); err != nil {
			return err
		}
		run.Processed = run.Stats.Processed()
		if run.Processed == run.Total {
			now := s.now().UTC()
			run.FinishedAt = &now
			run.Status = "success"
			if run.Stats.Failed+run.Stats.Quarantined > 0 {
				run.Status = "partial"
			}
		}
		if err := saveReplay(q, run); err != nil {
			return err
		}
		if err := s.fenceGrant(ctx, q, "replay", run.ID, run.SchedulingGeneration); err != nil {
			return err
		}
		if run.FinishedAt == nil {
			return s.emitReplay(ctx, run, run.Processed)
		}
		return releaseReplayPin(q, run.ID)
	})
	return run, err
}

func (s *syncEngineStore) applyReplayMembers(ctx context.Context, q *gorm.DB, run *syncmodule.ReplayRun, job repo.Job, mapping *ingestion.Compiled, members []replayMember) error {
	disposedAt := s.now().UTC()
	for i := range members {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.applyReplayMember(ctx, q, run, job, mapping, &members[i]); err != nil {
			return err
		}
	}
	return persistReplayOutcomes(q, run.ID, members, disposedAt)
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
	human, err := s.Authorize(ctx, run.TenantID, run.TriggeredBy, permission.SyncReplayCreate)
	if err != nil {
		return err
	}
	return s.checkHuman(human, job, permission.SyncReplayCreate)
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
	}
	return loadReplayPayloads(q, r, rows)
}

func loadReplayPayloads(q *gorm.DB, r syncmodule.ReplayRun, rows []replayMember) ([]replayMember, error) {
	keys := make([][]any, len(rows))
	for i := range rows {
		keys[i] = []any{rows[i].FetchedAt, rows[i].ID}
	}
	var payloads []replayMember
	err := q.Table("replay_run_item m").Select("m.id,m.fetched_at,p.payload").Joins(`LEFT JOIN raw_payload p ON p.id=m.id AND p.fetched_at=m.fetched_at AND p.external_id=m.external_id AND p.payload_sha256=m.payload_sha256 AND p.tenant_id=? AND p.platform_id=? AND p.shop_id=? AND p.resource_type=?`, r.TenantID, r.PlatformID, r.ShopID, r.ResourceType).Where("m.run_id=? AND (m.fetched_at,m.id) IN ?", r.ID, keys).Order("m.fetched_at,m.id").Find(&payloads).Error
	if err != nil {
		return nil, err
	}
	if len(payloads) != len(rows) {
		return nil, errors.New("replay raw unavailable; checkpoint retained")
	}
	for i := range rows {
		if len(payloads[i].Payload) == 0 {
			logger.From(q.Statement.Context).ErrorContext(q.Statement.Context, "replay raw unavailable; checkpoint retained", "module", "sync", "action", "replay_raw_unavailable", "run_id", r.ID, "raw_id", rows[i].ID)
			return nil, errors.New("replay raw unavailable; checkpoint retained")
		}
		rows[i].Payload = payloads[i].Payload
	}
	return rows, nil
}

func (s *syncEngineStore) applyReplayMember(ctx context.Context, q *gorm.DB, r *syncmodule.ReplayRun, job repo.Job, mapping *ingestion.Compiled, m *replayMember) error {
	handler := s.handlers[r.ResourceType]
	if handler == nil {
		return errors.New("replay resource handler unavailable")
	}
	ref := ingestion.RawRef{ID: m.ID, FetchedAt: m.FetchedAt, ExternalID: m.ExternalID}
	payload := m.Payload
	if r.ResourceType == "order" {
		opened, err := s.rawProtector.Open(ingestion.ProtectedRawRef{RawIdentity: ingestion.RawIdentity{TenantID: r.TenantID, PlatformID: r.PlatformID}, ShopID: r.ShopID, ID: m.ID, ExternalID: m.ExternalID, PayloadHash: m.PayloadSHA256, FetchedAt: m.FetchedAt}, payload)
		if err != nil {
			return err
		}
		defer clear(opened.Payload)
		defer clear(opened.RequestMeta)
		payload, ref.ExternalID = opened.Payload, opened.ExternalID
	}
	item := syncmodule.InterpretReplay(r.TenantID, r.PlatformID, r.ShopID, mapping, ref, payload, handler)
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
	m.Outcome, m.ReasonCode = outcome, item.Reason
	key, err := json.Marshal(syncmodule.ReplayKey{FetchedAt: m.FetchedAt.UTC(), ID: m.ID})
	r.CheckpointKey = key
	return err
}

func persistReplayOutcomes(q *gorm.DB, run int64, members []replayMember, disposedAt time.Time) error {
	rows := make([]map[string]any, len(members))
	for i, member := range members {
		rows[i] = map[string]any{"id": member.ID, "fetched_at": member.FetchedAt, "outcome": member.Outcome, "reason_code": member.ReasonCode}
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	result := q.Exec(`UPDATE replay_run_item m SET outcome=d.outcome,reason_code=d.reason_code,disposed_at=?
 FROM jsonb_to_recordset(?::jsonb) AS d(id bigint,fetched_at timestamptz,outcome text,reason_code text)
 WHERE m.run_id=? AND m.id=d.id AND m.fetched_at=d.fetched_at AND m.outcome IS NULL`, disposedAt, string(raw), run)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(members)) {
		return errors.New("replay member changed before disposition commit")
	}
	return nil
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

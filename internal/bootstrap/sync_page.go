package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *syncEngineStore) CommitPage(ctx context.Context, page repo.Page) (repo.Run, error) {
	if err := engineQuery("SyncEngine.CommitPage"); err != nil {
		return repo.Run{}, err
	}
	a, err := s.capability(ctx, "sync.page")
	if err != nil {
		return repo.Run{}, err
	}
	if page.Run.ID != a.RunID() || page.Run.JobID != a.JobID() || page.Run.TenantID != a.TenantID() || len(page.Items) > 100 {
		return repo.Run{}, syncDenied()
	}
	var current repo.Run
	err = s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		var jobID int64
		if err := q.Table("sync_job").Select("id").Where("id=? AND tenant_id=? AND shop_id=? AND enabled=true", a.JobID(), a.TenantID(), a.ShopID()).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&jobID).Error; err != nil {
			return err
		}
		// Recheck authorization after Fetch and immediately before business writes.
		if _, err := s.capability(ctx, "sync.page"); err != nil {
			return err
		}
		if err := q.Table("sync_run").Select(syncRunColumns).Where("id=? AND tenant_id=? AND job_id=?", a.RunID(), a.TenantID(), a.JobID()).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&current).Error; err != nil {
			return err
		}
		if current.Stats.Sequence != page.Run.Stats.Sequence || !current.Stats.Pending {
			return nil
		}
		if current.Stats.ReplayID == 0 {
			if err := s.checkCursor(q, a, page.Before); err != nil {
				return err
			}
		}
		for _, item := range page.Items {
			if err := s.commitItem(q, a, &current, item); err != nil {
				return err
			}
		}
		current.Stats.Fetched += len(page.Items)
		current.Stats.Retries = 0
		current.Stats.Sequence++
		if page.HasMore && page.Yield {
			current.Stats.Dispatch++
		}
		current.Stats.Pending = page.HasMore
		current.Status = "running"
		current.ErrorCode, current.ErrorMsg = "", ""
		if current.Stats.Quarantined+current.Stats.Failed > 0 {
			current.Status = "partial"
			current.ErrorCode = "item_disposition"
			current.ErrorMsg = "records require review"
		}
		if !page.HasMore || page.Yield {
			current.Status = "partial"
			if !page.HasMore {
				now := time.Now().UTC()
				current.FinishedAt = &now
				if current.Stats.Quarantined+current.Stats.Failed == 0 {
					current.Status = "success"
				}
			}
		}
		if err := s.commitCheckpoint(q, a, current, page); err != nil {
			return err
		}
		if err := saveSyncRun(q, current); err != nil {
			return err
		}
		if page.HasMore && page.Yield {
			return s.emitRun(ctx, current, 0)
		}
		return nil
	})
	if errors.Is(err, errRunAuthorizationRevoked) {
		_, authErr := s.LoadWork(ctx, a.JobID(), a.RunID())
		err = errors.Join(err, authErr)
	}
	return current, err
}

func (s *syncEngineStore) checkCursor(q *gorm.DB, a scope.WorkAccess, before string) error {
	row := map[string]any{"tenant_id": a.TenantID(), "shop_id": a.ShopID(), "resource_type": a.Resource(), "cursor_type": "page_token", "cursor_value": ""}
	if err := q.Table("sync_cursor").Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error; err != nil {
		return err
	}
	var value string
	if err := q.Table("sync_cursor").Select("cursor_value").Where("tenant_id=? AND shop_id=? AND resource_type=?", a.TenantID(), a.ShopID(), a.Resource()).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&value).Error; err != nil {
		return err
	}
	if value != before {
		return errors.New("sync checkpoint changed")
	}
	return nil
}

func (s *syncEngineStore) commitItem(q *gorm.DB, a scope.WorkAccess, run *repo.Run, item repo.Disposition) error {
	m := item.Mapped
	if m.TenantID != a.TenantID() || m.ShopID != a.ShopID() || item.Raw.ID <= 0 || m.RawID != item.Raw.ID || m.ExternalID != item.Raw.ExternalID {
		return syncDenied()
	}
	var rawCount int64
	if err := q.Table("raw_dedup").Where("tenant_id=? AND shop_id=? AND resource_type=? AND raw_id=? AND external_id=? AND fetched_at=?", a.TenantID(), a.ShopID(), a.Resource(), item.Raw.ID, item.Raw.ExternalID, item.Raw.FetchedAt).Count(&rawCount).Error; err != nil {
		return err
	}
	if rawCount != 1 {
		return syncDenied()
	}
	if item.Reason != "" {
		return s.commitDisposition(q, a, run, item)
	}
	stats, err := (catalog.Upserter{}).Upsert(q.Statement.Context, q, ingestion.ResourceType(a.Resource()), []ingestion.Mapped{m})
	if err != nil {
		return err
	}
	if stats.Quarantined > 0 {
		return errors.New("uncommitted canonical disposition")
	}
	run.Stats.Created += stats.Inserted
	run.Stats.Updated += stats.Updated
	run.Stats.Skipped += stats.Skipped
	return nil
}

func (s *syncEngineStore) commitDisposition(q *gorm.DB, a scope.WorkAccess, run *repo.Run, item repo.Disposition) error {
	meta := repo.ReplayRaw{RawID: item.Raw.ID, FetchedAt: item.Raw.FetchedAt, MappingVersion: a.MappingVersion(), JobID: a.JobID(), RunID: a.RunID()}
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if item.Dead {
		if err := q.Table("dead_letter").Create(map[string]any{"tenant_id": a.TenantID(), "shop_id": a.ShopID(), "resource_type": a.Resource(), "external_id": item.Raw.ExternalID, "payload": json.RawMessage(raw), "reason": item.Reason}).Error; err != nil {
			return err
		}
		run.Stats.Failed++
	} else {
		switch item.Reason {
		case "missing_required", "type_mismatch", "invalid_value":
		default:
			return errors.New("invalid quarantine reason")
		}
		if err := q.Table("quarantine_record").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "raw_id"}, {Name: "reason_code"}}, DoNothing: true}).Create(map[string]any{"tenant_id": a.TenantID(), "shop_id": a.ShopID(), "resource_type": a.Resource(), "external_id": item.Raw.ExternalID, "raw_id": item.Raw.ID, "reason_code": item.Reason, "reason_detail": json.RawMessage(raw)}).Error; err != nil {
			return err
		}
		run.Stats.Quarantined++
	}
	return q.Table("sync_error").Create(map[string]any{"run_id": a.RunID(), "external_id": item.Raw.ExternalID, "stage": "map", "error_code": item.Reason, "message": "record requires review", "raw_id": item.Raw.ID, "retryable": false}).Error
}

func (s *syncEngineStore) commitCheckpoint(q *gorm.DB, a scope.WorkAccess, run repo.Run, page repo.Page) error {
	if run.Stats.ReplayID > 0 {
		if run.Stats.Failed+run.Stats.Quarantined > 0 {
			return nil
		}
		table, _, err := recordTable(run.Stats.ReplayKind)
		if err != nil {
			return err
		}
		return syncChanged(q.Table(table).Where("id=? AND tenant_id=? AND shop_id=? AND resource_type=? AND status='pending'", run.Stats.ReplayID, a.TenantID(), a.ShopID(), a.Resource()).Updates(map[string]any{"status": "replayed", "replayed_at": time.Now().UTC()}))
	}
	return syncChanged(q.Table("sync_cursor").Where("tenant_id=? AND shop_id=? AND resource_type=? AND cursor_value=?", a.TenantID(), a.ShopID(), a.Resource(), page.Before).Updates(map[string]any{"cursor_value": page.Next, "updated_at": time.Now().UTC()}))
}

func syncChanged(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("sync row changed or unavailable")
	}
	return nil
}

package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *exportStore) CanDeleteExportObject(ctx context.Context, key string) (bool, error) {
	if err := catalogQuery("Export.Cleanup"); err != nil {
		return false, err
	}
	tenant, id, attempt, valid := catalog.ParseExportObjectKey(key)
	if !valid {
		return false, errors.New("invalid export object key")
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var job catalog.ExportJob
	err := q.Select("id, tenant_id, attempt, status, object_key").Where("tenant_id=? AND id=?", tenant, id).Take(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if job.ObjectKey == key {
		return false, nil
	}
	if attempt == 0 {
		return true, nil
	}
	// The attempt counter only increases. Older attempts can never publish later.
	if attempt < job.Attempt {
		return true, nil
	}
	if job.Status == "succeeded" || job.Status == "expired" {
		return true, nil
	}
	return attempt == job.Attempt && job.Status == "failed", nil
}

func (s *exportStore) QueueExportCleanup(ctx context.Context, tenant int64, key string) error {
	keyTenant, id, _, valid := catalog.ParseExportObjectKey(key)
	if !valid || tenant != keyTenant {
		return errors.New("invalid cleanup event identity")
	}
	return s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		// Serialize retries of one key without duplicating a live cleanup obligation.
		if err := q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key).Error; err != nil {
			return err
		}
		var count int64
		if err := q.Table("outbox_event").Where("event_type='export.cleanup' AND status<>'sent' AND tenant_id=? AND payload @> ?::jsonb", tenant,
			gorm.Expr("jsonb_build_object('key',?::text)", key)).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		return s.emitExport(ctx, tenant, id, "export.cleanup", key)
	})
}

// ExpireExports atomically fences publication and persists deletion obligations.
// No storage IO occurs while holding the row locks. Expired jobs cannot restart.
func (s *exportStore) ExpireExports(ctx context.Context, tenant, id int64, now time.Time) error {
	if err := catalogQuery("Export.Cleanup"); err != nil {
		return err
	}
	return s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		query := q.Select("id, tenant_id, object_key, file_id").Where("status='succeeded' AND expires_at<=?", now)
		if tenant > 0 && id > 0 {
			query = query.Where("tenant_id=? AND id=?", tenant, id)
		}
		var jobs []catalog.ExportJob
		if err := query.Order("id").Limit(100).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Find(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			if err := q.Table("export_job").Where("id=? AND tenant_id=?", job.ID, job.TenantID).
				Updates(map[string]any{"status": "expired", "object_key": "", "file_id": nil}).Error; err != nil {
				return err
			}
			if job.FileID != nil {
				if err := q.Table("file_object").Where("id=? AND tenant_id=? AND object_key=? AND purpose='export'", *job.FileID, job.TenantID, job.ObjectKey).
					Delete(&struct{}{}).Error; err != nil {
					return err
				}
			}
			if job.ObjectKey != "" {
				if err := s.emitExport(ctx, job.TenantID, job.ID, "export.cleanup", job.ObjectKey); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"gorm.io/gorm"
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
	return s.database.Transaction(ctx, func(ctx context.Context) error { return s.emitExport(ctx, tenant, id, "export.cleanup", key) })
}

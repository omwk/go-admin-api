package bootstrap

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"gorm.io/gorm"
)

func (s *exportStore) exportCleanupBinding(ctx context.Context, tenant int64, key string, raw []byte) ([]byte, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row struct {
		ProviderID *int64
		Bucket     string
	}
	err := q.Table("file_object").Select("provider_id,bucket").Where("tenant_id=? AND object_key=? AND purpose='export'", tenant, key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return raw, nil
	}
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if row.ProviderID != nil {
		payload["provider_id"] = *row.ProviderID
	}
	payload["bucket"] = row.Bucket
	return json.Marshal(payload)
}

func (s *resolvedExportObjects) cleanupBinding(ctx context.Context, key string) (*storage.S3, bool, error) {
	tenant, _, _, valid := catalog.ParseExportObjectKey(key)
	if !valid {
		return nil, false, errors.New("invalid export cleanup key")
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row struct {
		ProviderID *int64
		Bucket     string
	}
	err := q.Raw(`SELECT provider_id,bucket FROM file_object WHERE tenant_id=? AND object_key=? AND purpose='export'
UNION ALL SELECT (payload->>'provider_id')::bigint,payload->>'bucket' FROM outbox_event
WHERE tenant_id=? AND event_type='export.cleanup' AND status<>'sent' AND payload->>'key'=? AND jsonb_exists(payload,'bucket') LIMIT 1`, tenant, key, tenant, key).Scan(&row).Error
	if err != nil {
		return nil, false, err
	}
	if row.Bucket == "" {
		return nil, false, nil
	}
	selected, err := s.resolver.Resolve(ctx, row.ProviderID)
	if err != nil {
		return nil, false, err
	}
	return selected.Object.WithBucket(row.Bucket), true, nil
}

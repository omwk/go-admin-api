package bootstrap

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/catalog/port"
	"gorm.io/gorm"
)

// resolvedExportObjects binds each attempt once and resolves published files by ID.
type resolvedExportObjects struct {
	database *db.DB
	resolver *storage.Resolver
}

func (s *resolvedExportObjects) BindExport(ctx context.Context) (port.ObjectStore, *int64, string, error) {
	selected, err := s.resolver.Default(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	return selected.Object, &selected.ProviderID, selected.Bucket, nil
}

func (s *resolvedExportObjects) published(ctx context.Context, key string) (*storage.S3, error) {
	tenant, _, _, valid := catalog.ParseExportObjectKey(key)
	if !valid {
		return nil, errors.New("invalid export storage key")
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row struct {
		ProviderID *int64
		Bucket     string
	}
	err := q.Table("file_object").Select("provider_id,bucket").Where("tenant_id=? AND object_key=? AND purpose='export'", tenant, key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, catalog.ExportNotFound()
	}
	if err != nil {
		return nil, err
	}
	selected, err := s.resolver.Resolve(ctx, row.ProviderID)
	if err != nil {
		return nil, err
	}
	return selected.Object.WithBucket(row.Bucket), nil
}

func (s *resolvedExportObjects) Put(ctx context.Context, key string, body io.Reader, size int64) (int64, error) {
	object, _, _, err := s.BindExport(ctx)
	if err != nil {
		return 0, err
	}
	return object.Put(ctx, key, body, size)
}

func (s *resolvedExportObjects) Get(ctx context.Context, key string) (port.Object, error) {
	object, err := s.published(ctx, key)
	if err != nil {
		return port.Object{}, err
	}
	return object.Get(ctx, key)
}

func (s *resolvedExportObjects) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	object, err := s.published(ctx, key)
	if err != nil {
		return "", err
	}
	return object.PresignGet(ctx, key, ttl)
}

func (s *resolvedExportObjects) each(ctx context.Context, visit func(*storage.S3) error) error {
	providers, err := (&storageStore{database: s.database}).Providers(ctx)
	if err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var historical []struct {
		ProviderID int64
		Bucket     string
	}
	if err := q.Raw(`SELECT DISTINCT provider_id,bucket FROM file_object WHERE provider_id IS NOT NULL
UNION SELECT DISTINCT (payload->>'provider_id')::bigint AS provider_id,payload->>'bucket' AS bucket
FROM outbox_event WHERE event_type='export.cleanup' AND status<>'sent' AND jsonb_exists(payload,'provider_id')`).Scan(&historical).Error; err != nil {
		return err
	}
	seen := map[int64]map[string]bool{}
	for _, p := range providers {
		seen[p.ID] = map[string]bool{p.Bucket: true}
	}
	for _, row := range historical {
		if seen[row.ProviderID] == nil {
			seen[row.ProviderID] = map[string]bool{}
		}
		seen[row.ProviderID][row.Bucket] = true
	}
	var failures error
	for id, buckets := range seen {
		selected, err := s.resolver.Resolve(ctx, &id)
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		for bucket := range buckets {
			failures = errors.Join(failures, visit(selected.Object.WithBucket(bucket)))
		}
	}
	return failures
}

// Delete visits historical providers too: expired metadata may already be removed.
func (s *resolvedExportObjects) Delete(ctx context.Context, key string) error {
	object, found, err := s.cleanupBinding(ctx, key)
	if err != nil {
		return err
	}
	if found {
		return object.Delete(ctx, key)
	}
	return s.each(ctx, func(object *storage.S3) error { return object.Delete(ctx, key) })
}

func (s *resolvedExportObjects) Scan(ctx context.Context, prefix string, visit func(port.ObjectInfo) error) error {
	return s.each(ctx, func(object *storage.S3) error { return object.Scan(ctx, prefix, visit) })
}

var _ port.ObjectStore = (*resolvedExportObjects)(nil)
var _ port.ObjectScanner = (*resolvedExportObjects)(nil)

package bootstrap

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"gorm.io/gorm"
)

type storageStore struct{ database *db.DB }

const storageColumns = "id,tenant_id,name,endpoint,port,bucket,use_ssl,path_style,region,access_key,secret_enc,public_base_url,is_default,enabled,created_at,updated_at"

func (s *storageStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	return s.database.Transaction(ctx, fn)
}
func (s *storageStore) LockProviders(ctx context.Context) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "storage:providers:1").Error
}
func (s *storageStore) Providers(ctx context.Context) ([]storage.Provider, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []storage.Provider{}
	err := q.Select(storageColumns).Where("tenant_id=?", 1).Order("id").Find(&rows).Error
	return rows, err
}
func (s *storageStore) Provider(ctx context.Context, id int64) (storage.Provider, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var p storage.Provider
	err := q.Select(storageColumns).Where("tenant_id=? AND id=?", 1, id).Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = errs.New(errs.ErrNotFound, 404, "资源不存在")
	}
	return p, err
}
func (s *storageStore) DefaultProvider(ctx context.Context) (storage.Provider, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var p storage.Provider
	err := q.Select(storageColumns).Where("tenant_id=? AND is_default AND enabled", 1).Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = storage.Unconfigured()
	}
	return p, err
}
func (s *storageStore) SaveProvider(ctx context.Context, p *storage.Provider) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if p.ID == 0 {
		return q.Create(p).Error
	}
	p.UpdatedAt = time.Now().UTC()
	result := q.Model(&storage.Provider{}).Where("tenant_id=? AND id=?", 1, p.ID).Select("name", "endpoint", "port", "bucket", "use_ssl", "path_style", "region", "access_key", "secret_enc", "public_base_url", "is_default", "enabled", "updated_at").Updates(p)
	return storageMutation(result)
}
func (s *storageStore) ClearDefault(ctx context.Context) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return q.Model(&storage.Provider{}).Where("tenant_id=? AND is_default", 1).Updates(map[string]any{"is_default": false, "updated_at": time.Now().UTC()}).Error
}
func (s *storageStore) ProviderReferenced(ctx context.Context, id int64) (bool, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var count int64
	err := q.Table("file_object").Where("provider_id=?", id).Count(&count).Error
	if err == nil && count == 0 {
		err = q.Table("outbox_event").Where("event_type IN ? AND status<>'sent' AND payload->>'provider_id'=?", []string{"export.cleanup", "file.cleanup"}, strconv.FormatInt(id, 10)).Count(&count).Error
	}
	return count > 0, err
}
func (s *storageStore) DeleteProvider(ctx context.Context, id int64) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return storageMutation(q.Where("tenant_id=? AND id=?", 1, id).Delete(&storage.Provider{}))
}

func storageMutation(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errs.New(errs.ErrNotFound, 404, "资源不存在")
	}
	return nil
}

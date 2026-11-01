package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
	"gorm.io/gorm"
)

type fileStore struct{ database *db.DB }

var _ repo.FileStore = (*fileStore)(nil)

func newFileStore(database *db.DB) *fileStore { return &fileStore{database: database} }

func (s *fileStore) FileTransaction(ctx context.Context, fn func(context.Context) error) error {
	return s.database.Transaction(ctx, fn)
}

func (s *fileStore) LockFileHash(ctx context.Context, tenantID int64, sha string) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	lockKey := fmt.Sprintf("file:%d:%s", tenantID, sha)
	if err := q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockKey).Error; err != nil {
		return fmt.Errorf("lock file hash: %w", err)
	}
	return nil
}

func (s *fileStore) FindFileByHash(ctx context.Context, tenantID int64, sha string) (model.FileObject, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row model.FileObject
	err := q.Select("id, tenant_id, bucket, object_key, original_name, mime, size_bytes, sha256, purpose, uploaded_by, created_at").
		Where("tenant_id = ? AND sha256 = ?", tenantID, sha).Order("id asc").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.FileObject{}, shop.ErrFileNotFound
	}
	return row, fileDBError(err)
}

func (s *fileStore) CreateFile(ctx context.Context, row *model.FileObject) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Select("tenant_id, bucket, object_key, original_name, mime, size_bytes, sha256, purpose, uploaded_by").Create(row)
	return fileDBError(result.Error)
}

func (s *fileStore) File(ctx context.Context, tenantID, id int64) (model.FileObject, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row model.FileObject
	err := q.Select("id, tenant_id, bucket, object_key, original_name, mime, size_bytes, sha256, purpose, uploaded_by, created_at").
		Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.FileObject{}, shop.ErrFileNotFound
	}
	return row, fileDBError(err)
}

func fileDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shop.ErrFileNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return shop.ErrFileNotFound
	}
	return fmt.Errorf("file metadata operation failed: %w", err)
}

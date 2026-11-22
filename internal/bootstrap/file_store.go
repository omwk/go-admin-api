package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/modules/file/model"
	"github.com/omwk/go-admin-api/internal/modules/file/repo"
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
	err := fileReadProjection(q).Where("file_object.tenant_id = ? AND file_object.sha256 = ?", tenantID, sha).
		Where("file_object.purpose <> 'avatar'").
		Order("file_object.id asc").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.FileObject{}, filemodule.ErrFileNotFound
	}
	return row, fileDBError(err)
}

func (s *fileStore) CreateFile(ctx context.Context, row *model.FileObject) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Select("tenant_id", "provider_id", "bucket", "object_key", "original_name", "mime", "size_bytes", "sha256", "purpose", "category", "thumb_key", "thumb_status", "thumb_attempts", "uploaded_by").Create(row)
	if result.Error != nil {
		return fileDBError(result.Error)
	}
	created, err := s.File(ctx, row.TenantID, row.ID)
	if err == nil {
		*row = created
		if row.ThumbStatus == "pending" {
			return s.queueThumbnail(ctx, *row)
		}
	}
	return err
}

func (s *fileStore) File(ctx context.Context, tenantID, id int64) (model.FileObject, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row model.FileObject
	err := fileReadProjection(q).Where("file_object.tenant_id = ? AND file_object.id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.FileObject{}, filemodule.ErrFileNotFound
	}
	return row, fileDBError(err)
}

func fileReadProjection(q *gorm.DB) *gorm.DB {
	return q.Model(&model.FileObject{}).
		Select("file_object.id,file_object.tenant_id,file_object.provider_id,file_object.bucket,file_object.object_key,file_object.original_name,file_object.mime,file_object.size_bytes,file_object.sha256,file_object.purpose,COALESCE(file_object.category,'') AS category,COALESCE(file_object.thumb_key,'') AS thumb_key,file_object.thumb_status,file_object.thumb_attempts,file_object.uploaded_by,file_object.created_at,COALESCE(uploader.username,'') AS uploaded_by_name").
		Joins("LEFT JOIN sys_user AS uploader ON uploader.id=file_object.uploaded_by AND uploader.tenant_id=file_object.tenant_id AND uploader.deleted_at IS NULL")
}

func fileDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return filemodule.ErrFileNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return errs.New(errs.ErrConflict, 409, "文件仍被引用")
	}
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return filemodule.ErrFileNotFound
	}
	return fmt.Errorf("file metadata operation failed: %w", err)
}

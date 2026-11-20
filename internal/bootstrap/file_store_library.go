package bootstrap

import (
	"context"
	"strings"

	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/modules/file/model"
)

func (s *fileStore) ListFiles(ctx context.Context, tenant int64, purpose, keyword string, page, size int) ([]model.FileObject, int64, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	q = q.Model(&model.FileObject{}).Where("file_object.tenant_id=?", tenant)
	if purpose != "" {
		q = q.Where("purpose=?", purpose)
	}
	if keyword != "" {
		escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(keyword)
		q = q.Where("original_name ILIKE ?", "%"+escaped+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []model.FileObject{}
	err := fileReadProjection(q).Order("file_object.created_at DESC,file_object.id DESC").Limit(size).Offset((page - 1) * size).Find(&rows).Error
	return rows, total, err
}
func (s *fileStore) DeleteFile(ctx context.Context, tenant, id int64) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Where("tenant_id=? AND id=?", tenant, id).Delete(&model.FileObject{})
	if result.Error != nil {
		return fileDBError(result.Error)
	}
	if result.RowsAffected == 0 {
		return filemodule.ErrFileNotFound
	}
	return nil
}

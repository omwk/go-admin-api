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
	q = q.Model(&model.FileObject{}).Where("tenant_id=?", tenant)
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
	err := q.Select("id,tenant_id,provider_id,bucket,object_key,original_name,mime,size_bytes,sha256,purpose,uploaded_by,created_at").Order("created_at DESC,id DESC").Limit(size).Offset((page - 1) * size).Find(&rows).Error
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

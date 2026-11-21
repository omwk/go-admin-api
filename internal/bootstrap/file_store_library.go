package bootstrap

import (
	"context"
	"strings"

	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/modules/file/model"
)

func (s *fileStore) ListFiles(ctx context.Context, tenant int64, category, keyword string, page, size int) ([]model.FileObject, int64, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	q = q.Model(&model.FileObject{}).Where("file_object.tenant_id=?", tenant)
	if category != "" {
		q = q.Where(fileCategorySQL+"=?", category)
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

// Keep legacy filtering consistent with file.Category without rewriting historical metadata.
const fileCategorySQL = `COALESCE(NULLIF(file_object.category,''),CASE
 WHEN lower(split_part(file_object.mime,';',1))='application/pdf' THEN 'document'
 WHEN lower(split_part(file_object.mime,';',1)) IN ('text/csv','application/vnd.openxmlformats-officedocument.spreadsheetml.sheet') THEN 'sheet'
 WHEN lower(split_part(file_object.mime,';',1))='application/zip' THEN 'archive'
 WHEN lower(file_object.mime) LIKE 'image/%' THEN 'image'
 WHEN lower(file_object.mime) LIKE 'text/%' OR lower(split_part(file_object.mime,';',1)) IN ('application/json','application/x-ndjson','application/xml','application/yaml') THEN 'text'
 ELSE 'other' END)`

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

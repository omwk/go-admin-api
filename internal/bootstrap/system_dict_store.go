package bootstrap

import (
	"context"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"gorm.io/gorm/clause"
)

const systemDictColumns = "id,tenant_id,type,name,status,builtin,created_at,updated_at"
const systemDictItemColumns = "id,tenant_id,dict_id,label,value,sort,status,color,extra,builtin"

type systemDictStore struct{ database *db.DB }

var _ system.DictStore = (*systemDictStore)(nil)

func newSystemDictStore(database *db.DB) *systemDictStore {
	return &systemDictStore{database: database}
}
func (s *systemDictStore) Transaction(ctx context.Context, run func(context.Context) error) error {
	return s.database.Transaction(ctx, run)
}

func (s *systemDictStore) LockDicts(ctx context.Context, tenant int64) error {
	if tenant <= 0 {
		return errs.New(errs.ErrNotFound, 404, "资源不存在")
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", fmt.Sprintf("system:dicts:%d", tenant)).Error
}

func (s *systemDictStore) Dictionary(ctx context.Context, tenant, id int64, lock bool) (system.Dict, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	query := q.Select(systemDictColumns).Where("tenant_id=? AND id=?", tenant, id)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var row system.Dict
	err := query.Take(&row).Error
	return row, adminDBError(err)
}

func (s *systemDictStore) DictionaryByType(ctx context.Context, tenant int64, kind string) (system.Dict, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row system.Dict
	err := q.Select(systemDictColumns).Where("tenant_id=? AND type=?", tenant, kind).Take(&row).Error
	return row, adminDBError(err)
}

func (s *systemDictStore) Dictionaries(ctx context.Context, tenant int64, page, size int) (system.Page[system.Dict], error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := system.Page[system.Dict]{List: []system.Dict{}, Page: page, PageSize: size}
	if err := q.Model(&system.Dict{}).Where("tenant_id=?", tenant).Count(&result.Total).Error; err != nil {
		return result, err
	}
	err := q.Select(systemDictColumns).Where("tenant_id=?", tenant).Order("created_at DESC,id DESC").Offset((page - 1) * size).Limit(size).Find(&result.List).Error
	return result, err
}

func (s *systemDictStore) SaveDictionary(ctx context.Context, row *system.Dict, create bool) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if create {
		return adminDBError(q.Create(row).Error)
	}
	result := q.Model(&system.Dict{}).Where("tenant_id=? AND id=?", row.TenantID, row.ID).Updates(map[string]any{"type": row.Type, "name": row.Name, "status": row.Status})
	if err := adminMutation(result); err != nil {
		return err
	}
	return adminDBError(q.Select(systemDictColumns).Where("tenant_id=? AND id=?", row.TenantID, row.ID).Take(row).Error)
}

func (s *systemDictStore) DeleteDictionary(ctx context.Context, tenant, id int64) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return adminMutation(q.Where("tenant_id=? AND id=? AND builtin=false", tenant, id).Delete(&system.Dict{}))
}

func (s *systemDictStore) DictionaryItems(ctx context.Context, tenant, id int64, page, size int, active bool) (system.Page[system.DictItem], error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := system.Page[system.DictItem]{List: []system.DictItem{}, Page: page, PageSize: size}
	query := q.Model(&system.DictItem{}).Where("tenant_id=? AND dict_id=?", tenant, id)
	if active {
		query = query.Where("status=?", "active")
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	query = query.Select(systemDictItemColumns).Order("sort ASC,id ASC")
	if size > 0 {
		query = query.Offset((page - 1) * size).Limit(size)
	}
	err := query.Find(&result.List).Error
	return result, err
}

func (s *systemDictStore) DictionaryItem(ctx context.Context, tenant, id int64) (system.DictItem, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row system.DictItem
	err := q.Select(systemDictItemColumns).Where("tenant_id=? AND id=?", tenant, id).Take(&row).Error
	return row, adminDBError(err)
}

func (s *systemDictStore) SaveDictionaryItem(ctx context.Context, row *system.DictItem, create bool) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if create {
		return adminDBError(q.Create(row).Error)
	}
	return adminMutation(q.Model(&system.DictItem{}).Where("tenant_id=? AND id=? AND dict_id=?", row.TenantID, row.ID, row.DictID).
		Updates(map[string]any{"label": row.Label, "value": row.Value, "sort": row.Sort, "status": row.Status, "color": row.Color, "extra": row.Extra}))
}

func (s *systemDictStore) DeleteDictionaryItem(ctx context.Context, tenant, id int64) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return adminMutation(q.Where("tenant_id=? AND id=? AND builtin=false", tenant, id).Delete(&system.DictItem{}))
}

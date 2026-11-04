package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"gorm.io/gorm"
)

type categoryManagementStore struct{ database *db.DB }

func NewCategoryManagementStore(database *db.DB) catalog.CategoryManagementStore {
	return &categoryManagementStore{database: database}
}

func (s *categoryManagementStore) Authorize(ctx context.Context, tenantID, userID int64, _ string) (context.Context, error) {
	return newAdminStore(s.database).Authorize(ctx, tenantID, userID)
}

func (s *categoryManagementStore) ListCategories(ctx context.Context, tenantID int64) ([]catalog.Category, error) {
	if err := catalogQuery("Catalog.ListCategories"); err != nil {
		return nil, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []catalog.Category{}
	err := q.Select("id, tenant_id, parent_id, name, path, level, sort, status, created_at, updated_at").
		Where("tenant_id=?", tenantID).Order("path, id").Find(&rows).Error
	return rows, err
}

func (s *categoryManagementStore) ListCategoryMappingPool(ctx context.Context, tenantID, platformID int64, page, size int, matched ...string) ([]catalog.MappingPoolItem, int64, error) {
	if err := catalogQuery("Catalog.ListCategoryMappingPool"); err != nil {
		return nil, 0, err
	}
	state := "false"
	if len(matched) > 0 {
		state = matched[0]
	}
	if state != "false" && state != "true" && state != "all" {
		return nil, 0, errs.Invalid(errors.New("invalid matched filter"))
	}
	return s.loadCategoryMappingPool(ctx, tenantID, platformID, page, size, state)
}

func (s *categoryManagementStore) loadCategoryMappingPool(ctx context.Context, tenantID, platformID int64, page, size int, matched string) ([]catalog.MappingPoolItem, int64, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	type row struct {
		MappingID                                int64
		TenantID                                 int64
		PlatformID                               int64
		PlatformCategoryID                       int64
		CategoryID                               *int64
		Confidence                               float64
		Source                                   string
		CreatedAt, UpdatedAt                     time.Time
		ExternalID, ParentExternalID, Name, Path string
		IsLeaf                                   bool
		SyncedAt                                 time.Time
	}
	base := q.Table("category_mapping m").Joins("JOIN platform_category p ON p.tenant_id=m.tenant_id AND p.id=m.platform_category_id").
		Where("m.tenant_id=?", tenantID)
	if platformID > 0 {
		base = base.Where("m.platform_id=?", platformID)
	}
	if matched == "false" {
		base = base.Where("m.category_id IS NULL")
	}
	if matched == "true" {
		base = base.Where("m.category_id IS NOT NULL")
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []row
	err := base.Select("m.id AS mapping_id, m.tenant_id, m.platform_id, m.platform_category_id, m.category_id, m.confidence, m.source, m.created_at, m.updated_at, p.external_id, p.parent_external_id, p.name, p.path, p.is_leaf, p.synced_at").
		Order("p.path, p.external_id, m.id").Offset((page - 1) * size).Limit(size).Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	out := make([]catalog.MappingPoolItem, 0, len(rows))
	for _, row := range rows {
		item := catalog.MappingPoolItem{
			Mapping:  catalog.CategoryMapping{ID: row.MappingID, TenantID: row.TenantID, PlatformID: row.PlatformID, PlatformCategoryID: row.PlatformCategoryID, CategoryID: row.CategoryID, Confidence: row.Confidence, Source: row.Source, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt},
			Platform: catalog.PlatformCategory{TenantID: row.TenantID, PlatformID: row.PlatformID, ID: row.PlatformCategoryID, ExternalID: row.ExternalID, ParentExternalID: row.ParentExternalID, Name: row.Name, Path: row.Path, IsLeaf: row.IsLeaf, SyncedAt: row.SyncedAt},
		}
		item.Candidates, err = s.categoryCandidates(ctx, tenantID, item.Platform)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, item)
	}
	return out, total, nil
}

func (s *categoryManagementStore) categoryCandidates(ctx context.Context, tenantID int64, platform catalog.PlatformCategory) ([]catalog.CategoryMappingCandidate, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var exact []catalog.Category
	if err := q.Select("id, name, path").Where("tenant_id=? AND path=? AND name=? AND status='active'", tenantID, platform.Path, platform.Name).Order("id").Find(&exact).Error; err != nil {
		return nil, err
	}
	if len(exact) > 0 {
		out := make([]catalog.CategoryMappingCandidate, 0, len(exact))
		for _, row := range exact {
			out = append(out, catalog.CategoryMappingCandidate{CategoryID: row.ID, Name: row.Name, Path: row.Path, Confidence: 0.9})
		}
		return out, nil
	}
	return catalog.ParentCandidates(ctx, q, tenantID, platform)
}

func (s *categoryManagementStore) SetManualCategoryMapping(ctx context.Context, tenantID, mappingID, categoryID int64, operations ...*idem.Operation) error {
	if err := catalogQuery("Catalog.SetManualCategoryMapping"); err != nil {
		return err
	}
	return s.categoryMutation(ctx, tenantID, operations, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		var categoryIDCheck int64
		if err := q.Table("category").Select("id").Where("tenant_id=? AND id=? AND status='active'", tenantID, categoryID).Take(&categoryIDCheck).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errs.New(errs.ErrInvalidParam, 400, "类目不存在或已停用")
			}
			return err
		}
		result := q.Table("category_mapping").Where("tenant_id=? AND id=?", tenantID, mappingID).
			Updates(map[string]any{"category_id": categoryID, "confidence": 1.0, "source": "manual", "updated_at": gorm.Expr("now()")})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errs.New(40401, 404, "类目映射不存在")
		}
		return nil
	})
}

func catalogQuery(name string) error {
	_, err := scopeRegistryQuery(name)
	return err
}

var _ catalog.CategoryManagementStore = (*categoryManagementStore)(nil)

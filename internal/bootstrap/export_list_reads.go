package bootstrap

import (
	"context"
	"encoding/json"

	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"gorm.io/gorm"
)

const exportListColumns = "id,status,attempt,row_total,row_count,expires_at,created_at,started_at,finished_at"

func (s *exportStore) ListExports(ctx context.Context, tenant, user int64, page catalog.ExportListQuery) (catalog.ExportPage, error) {
	if _, err := scopeRegistryQuery("Export.List"); err != nil {
		return catalog.ExportPage{}, err
	}
	return s.listExportJobs(ctx, tenant, user, "products", page, nil)
}

func (s *exportStore) ListOrderExports(ctx context.Context, tenant, user int64, page catalog.ExportListQuery) (catalog.ExportPage, error) {
	if _, err := scopeRegistryQuery("Order.ExportList"); err != nil {
		return catalog.ExportPage{}, err
	}
	shops, err := s.orderExportShops(ctx, tenant)
	if err != nil {
		return catalog.ExportPage{}, err
	}
	if shops == nil {
		shops = []int64{}
	}
	allowed, err := json.Marshal(shops)
	if err != nil {
		return catalog.ExportPage{}, err
	}
	return s.listExportJobs(ctx, tenant, user, "orders", page, allowed)
}

func (s *exportStore) listExportJobs(ctx context.Context, tenant, user int64, kind string, page catalog.ExportListQuery, allowed []byte) (catalog.ExportPage, error) {
	access, err := scope.From(ctx)
	if err != nil || access.Tenant != tenant || access.User != user {
		return catalog.ExportPage{}, scope.Denied()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	q = q.Model(&catalog.ExportJob{}).Where("tenant_id=? AND requested_by=? AND kind=?", tenant, user, kind)
	if kind == "orders" {
		q = q.Where("filters->>'fields_version' = '1' AND ?::jsonb @> (filters->'shop_ids')", string(allowed))
	}
	result := catalog.ExportPage{List: []catalog.ExportJob{}, Page: page.Page, PageSize: page.PageSize}
	if err := q.Session(&gorm.Session{}).Count(&result.Total).Error; err != nil {
		return result, err
	}
	err = q.Select(exportListColumns).Order("created_at DESC,id DESC").
		Limit(page.PageSize).Offset((page.Page - 1) * page.PageSize).Find(&result.List).Error
	return result, err
}

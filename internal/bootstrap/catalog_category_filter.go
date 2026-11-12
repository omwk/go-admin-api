package bootstrap

import (
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"gorm.io/gorm"
)

func categoryMatchQuery(q *gorm.DB, tenant int64, column string, filter catalog.ProductFilter) *gorm.DB {
	match := q.Session(&gorm.Session{NewDB: true}).Where(column+"=?", filter.CategoryID)
	if !filter.IncludeDescendants {
		return match
	}
	// Resolve the selected category's actual path inside the same SQL statement.
	// The separator excludes prefix siblings, and both sides stay in the tenant.
	descendants := q.Session(&gorm.Session{NewDB: true}).Table("category descendant").Select("descendant.id").
		Joins("JOIN category ancestor ON ancestor.tenant_id=descendant.tenant_id").
		Where("descendant.tenant_id=? AND ancestor.id=? AND descendant.path LIKE ancestor.path || '.%'", tenant, filter.CategoryID)
	return match.Or(column+" IN (?)", descendants)
}

func mappedCategoryQuery(q *gorm.DB, tenant int64, filter catalog.ProductFilter) *gorm.DB {
	return q.Session(&gorm.Session{NewDB: true}).Table("listing cl").Select("1").
		Joins("JOIN platform_category pc ON pc.tenant_id=cl.tenant_id AND pc.platform_id=cl.platform_id").
		Joins("JOIN category_mapping cm ON cm.tenant_id=pc.tenant_id AND cm.platform_id=pc.platform_id AND cm.platform_category_id=pc.id").
		Where("cl.tenant_id=? AND cl.ext @> jsonb_build_object('category_id',pc.external_id)", tenant).
		Where(categoryMatchQuery(q, tenant, "cm.category_id", filter))
}

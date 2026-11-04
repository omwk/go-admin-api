package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"gorm.io/gorm"
)

// NewProductStore constructs the tenant-scoped catalog persistence adapter.
func NewProductStore(database *db.DB) catalog.ProductStore { return &productStore{database: database} }

func (s *productStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	return s.database.Transaction(ctx, fn)
}

func (s *productStore) ListProducts(ctx context.Context, tenant int64, query catalog.ProductListQuery, cursor *catalog.ProductCursorPosition) ([]catalog.ProductSummary, int64, bool, error) {
	if err := catalogQuery("Catalog.ListProducts"); err != nil {
		return nil, 0, false, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	base := productFilterQuery(q.Table("product_spu p"), tenant, query)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, false, err
	}
	base = productFilterQuery(q.Table("product_spu p"), tenant, query)
	if cursor != nil {
		base = base.Where("(p.updated_at, p.id) < (?, ?)", cursor.UpdatedAt, cursor.ID)
	} else {
		base = base.Offset((query.Page - 1) * query.PageSize)
	}
	type productListRow struct {
		ID            int64
		Title         string
		MainImage     string
		PriceMinCents int64
		PriceMaxCents int64
		SKUCount      int
		Status        string
		Source        string
		UpdatedAt     time.Time
		ShopNames     json.RawMessage `gorm:"column:shop_names"`
	}
	rows := make([]productListRow, 0, query.PageSize+1)
	err := base.Select("p.id, p.title, p.main_image, p.price_min_cents, p.price_max_cents, p.sku_count, p.status, p.source, p.updated_at, COALESCE(json_agg(DISTINCT sh.name) FILTER (WHERE sh.name IS NOT NULL), '[]'::json) AS shop_names").
		Joins("LEFT JOIN listing l ON l.tenant_id=p.tenant_id AND l.spu_id=p.id").
		Joins("LEFT JOIN shop sh ON sh.tenant_id=l.tenant_id AND sh.id=l.shop_id").
		Group("p.id").Order(productOrder(query.Sort)).Limit(query.PageSize + 1).Find(&rows).Error
	if err != nil {
		return nil, 0, false, err
	}
	more := len(rows) > query.PageSize
	if more {
		rows = rows[:query.PageSize]
	}
	out := make([]catalog.ProductSummary, 0, len(rows))
	for _, row := range rows {
		item := catalog.ProductSummary{ID: row.ID, Title: row.Title, MainImage: row.MainImage, PriceMinCents: row.PriceMinCents,
			PriceMaxCents: row.PriceMaxCents, SKUCount: row.SKUCount, Status: row.Status, Source: row.Source, UpdatedAt: row.UpdatedAt, ShopNames: []string{}}
		if err := json.Unmarshal(row.ShopNames, &item.ShopNames); err != nil {
			return nil, 0, false, fmt.Errorf("decode product shop names: %w", err)
		}
		out = append(out, item)
	}
	return out, total, more, nil
}

func (s *productStore) Product(ctx context.Context, tenant, id int64) (catalog.ProductDetail, error) {
	if err := catalogQuery("Catalog.Product"); err != nil {
		return catalog.ProductDetail{}, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var spu catalog.ProductSPU
	err := q.Select("id, tenant_id, code, title, subtitle, brand, category_id, main_image, description, status, source, attributes, price_min_cents, price_max_cents, sku_count, version, created_at, updated_at").
		Where("tenant_id=? AND id=? AND deleted_at IS NULL", tenant, id).Take(&spu).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return catalog.ProductDetail{}, productNotFound()
	}
	if err != nil {
		return catalog.ProductDetail{}, err
	}
	detail := catalog.ProductDetail{Product: productView(spu), SKUs: []catalog.ProductSKUView{}, Listings: []catalog.ProductListingDetail{}, Media: []catalog.ProductMedia{}}
	if err := s.loadProductSKUs(q, tenant, id, &detail); err != nil {
		return catalog.ProductDetail{}, err
	}
	if err := s.loadProductListings(q, tenant, id, &detail); err != nil {
		return catalog.ProductDetail{}, err
	}
	err = q.Select("id, tenant_id, spu_id, sku_id, type, url, sort, created_at").Where("tenant_id=? AND spu_id=?", tenant, id).Order("sort, id").Find(&detail.Media).Error
	return detail, err
}

func (s *productStore) loadProductSKUs(q *gorm.DB, tenant, id int64, detail *catalog.ProductDetail) error {
	var skus []catalog.ProductSKU
	err := q.Select("id, tenant_id, spu_id, code, barcode, title, price_cents, currency, attributes, status").
		Where("tenant_id=? AND spu_id=? AND deleted_at IS NULL", tenant, id).Order("id").Find(&skus).Error
	if err != nil {
		return err
	}
	for _, row := range skus {
		detail.SKUs = append(detail.SKUs, catalog.ProductSKUView{ID: row.ID, SPUID: row.SPUID, Code: row.Code, Barcode: row.Barcode,
			Title: row.Title, PriceCents: row.PriceCents, Currency: row.Currency, Attributes: row.Attributes, Status: row.Status})
	}
	return nil
}

func (s *productStore) loadProductListings(q *gorm.DB, tenant, id int64, detail *catalog.ProductDetail) error {
	var listings []catalog.Listing
	err := q.Select("id, tenant_id, shop_id, platform_id, spu_id, external_product_id, title, price_cents, currency, status, stock_total, synced_at, ext").
		Where("tenant_id=? AND spu_id=?", tenant, id).Order("id").Find(&listings).Error
	if err != nil {
		return err
	}
	for _, row := range listings {
		view := catalog.ProductListingDetail{ListingView: catalog.SafeListingView(row), SKUs: []catalog.ListingSKUView{}}
		var listingSKUs []catalog.ListingSKU
		if err := q.Select("id, sku_id, external_sku_id, title, price_cents, stock, ext, synced_at").Where("tenant_id=? AND listing_id=?", tenant, row.ID).Order("id").Find(&listingSKUs).Error; err != nil {
			return err
		}
		for _, sku := range listingSKUs {
			detail.ReportedStock += int64(sku.Stock)
			view.SKUs = append(view.SKUs, catalog.SafeListingSKUView(sku))
		}
		detail.Listings = append(detail.Listings, view)
	}
	return nil
}

func (s *productStore) ListListings(ctx context.Context, tenant int64, query catalog.ProductListQuery) ([]catalog.ListingView, int64, error) {
	if err := catalogQuery("Catalog.ListListings"); err != nil {
		return nil, 0, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	base := listingFilterQuery(q.Model(&catalog.Listing{}), tenant, query)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []catalog.Listing{}
	err := listingFilterQuery(q.Model(&catalog.Listing{}), tenant, query).
		Select("id, shop_id, platform_id, spu_id, external_product_id, title, price_cents, currency, status, stock_total, synced_at, ext").
		Order("synced_at DESC, id DESC").Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	out := make([]catalog.ListingView, 0, len(rows))
	for _, row := range rows {
		out = append(out, catalog.SafeListingView(row))
	}
	return out, total, nil
}

func productView(row catalog.ProductSPU) catalog.ProductView {
	return catalog.ProductView{ID: row.ID, TenantID: row.TenantID, Code: row.Code, Title: row.Title, Subtitle: row.Subtitle, Brand: row.Brand,
		CategoryID: row.CategoryID, MainImage: row.MainImage, Description: row.Description, Status: row.Status, Source: row.Source,
		Attributes: row.Attributes, PriceMinCents: row.PriceMinCents, PriceMaxCents: row.PriceMaxCents, SKUCount: row.SKUCount,
		Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func productFilterQuery(q *gorm.DB, tenant int64, filter catalog.ProductListQuery) *gorm.DB {
	q = q.Where("p.tenant_id=? AND p.deleted_at IS NULL", tenant)
	if filter.Keyword != "" {
		q = q.Where("(p.title ILIKE ? OR p.code = ?)", "%"+filter.Keyword+"%", filter.Keyword)
	}
	if filter.Status != "" {
		q = q.Where("p.status=?", filter.Status)
	}
	if filter.CategoryID > 0 {
		q = q.Where(`(p.category_id=? OR EXISTS (
SELECT 1 FROM listing cl JOIN platform_category pc ON pc.tenant_id=cl.tenant_id AND pc.platform_id=cl.platform_id
JOIN category_mapping cm ON cm.tenant_id=pc.tenant_id AND cm.platform_id=pc.platform_id AND cm.platform_category_id=pc.id
WHERE cl.tenant_id=p.tenant_id AND cl.spu_id=p.id AND cl.ext @> jsonb_build_object('category_id',pc.external_id) AND cm.category_id=?))`, filter.CategoryID, filter.CategoryID)
	}
	if filter.PriceMinCents != nil {
		q = q.Where("p.price_max_cents >= ?", *filter.PriceMinCents)
	}
	if filter.PriceMaxCents != nil {
		q = q.Where("p.price_min_cents <= ?", *filter.PriceMaxCents)
	}
	if filter.UpdatedFrom != nil {
		q = q.Where("p.updated_at >= ?", *filter.UpdatedFrom)
	}
	if filter.UpdatedTo != nil {
		q = q.Where("p.updated_at < ?", *filter.UpdatedTo)
	}
	if filter.HasPrice != nil {
		if *filter.HasPrice {
			q = q.Where("p.price_max_cents > 0")
		} else {
			q = q.Where("p.price_max_cents = 0")
		}
	}
	if filter.IsManual != nil {
		if *filter.IsManual {
			q = q.Where("p.source='manual'")
		} else {
			q = q.Where("p.source <> 'manual'")
		}
	}
	if filter.ShopID > 0 || filter.PlatformID > 0 {
		exists := q.Session(&gorm.Session{NewDB: true}).Table("listing l").Select("1").Where("l.tenant_id=p.tenant_id AND l.spu_id=p.id")
		if filter.ShopID > 0 {
			exists = exists.Where("l.shop_id=?", filter.ShopID)
		}
		if filter.PlatformID > 0 {
			exists = exists.Where("l.platform_id=?", filter.PlatformID)
		}
		q = q.Where("EXISTS (?)", exists)
	}
	return q
}

func listingFilterQuery(q *gorm.DB, tenant int64, filter catalog.ProductListQuery) *gorm.DB {
	q = q.Where("tenant_id=?", tenant)
	if filter.Keyword != "" {
		q = q.Where("(title ILIKE ? OR external_product_id = ?)", "%"+filter.Keyword+"%", filter.Keyword)
	}
	if filter.ShopID > 0 {
		q = q.Where("shop_id=?", filter.ShopID)
	}
	if filter.PlatformID > 0 {
		q = q.Where("platform_id=?", filter.PlatformID)
	}
	if filter.Status != "" {
		q = q.Where("status=?", filter.Status)
	}
	return q
}

func productOrder(sort string) string {
	orders := map[string]string{"updated_at": "p.updated_at ASC, p.id ASC", "-updated_at": "p.updated_at DESC, p.id DESC",
		"title": "p.title ASC, p.id ASC", "-title": "p.title DESC, p.id DESC", "price_cents": "p.price_min_cents ASC, p.id ASC",
		"-price_cents": "p.price_min_cents DESC, p.id DESC", "price_min_cents": "p.price_min_cents ASC, p.id ASC",
		"-price_min_cents": "p.price_min_cents DESC, p.id DESC"}
	return orders[sort]
}

var _ catalog.ProductStore = (*productStore)(nil)

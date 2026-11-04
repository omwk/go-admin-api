package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type productStore struct{ database *db.DB }

func (s *productStore) CreateSPU(ctx context.Context, row *catalog.ProductSPU) error {
	if err := catalogQuery("Catalog.CreateSPU"); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return q.Create(row).Error
}

func (s *productStore) UpdateSPU(ctx context.Context, tenant, id int64, version int, request catalog.ProductUpdateRequest) error {
	if err := catalogQuery("Catalog.UpdateSPU"); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Model(&catalog.ProductSPU{}).Where("tenant_id=? AND id=? AND deleted_at IS NULL AND source='manual' AND version=?", tenant, id, version).
		Updates(map[string]any{"title": request.Title, "subtitle": request.Subtitle, "brand": request.Brand, "category_id": request.CategoryID,
			"main_image": request.MainImage, "description": request.Description, "status": request.Status, "attributes": catalogJSON(request.Attributes),
			"ext": catalogJSON(request.Ext), "version": gorm.Expr("version + 1"), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var existing catalog.ProductSPU
	if err := q.Select("source, version").Where("tenant_id=? AND id=? AND deleted_at IS NULL", tenant, id).Take(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return productNotFound()
	} else if err != nil {
		return err
	} else if existing.Source != "manual" {
		return productConflict("仅手工商品允许编辑")
	}
	return productVersionConflict()
}

func (s *productStore) ArchiveSPU(ctx context.Context, tenant, id int64, version int) error {
	if err := catalogQuery("Catalog.ArchiveSPU"); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	now := time.Now().UTC()
	result := q.Model(&catalog.ProductSPU{}).Where("tenant_id=? AND id=? AND deleted_at IS NULL AND version=?", tenant, id, version).
		Updates(map[string]any{"deleted_at": now, "status": "archived", "version": gorm.Expr("version + 1"), "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var existing catalog.ProductSPU
		if err := q.Select("id, version").Where("tenant_id=? AND id=? AND deleted_at IS NULL", tenant, id).Take(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return productNotFound()
		} else if err != nil {
			return err
		}
		return productVersionConflict()
	}
	return nil
}

func (s *productStore) ReplaceManualSKUs(ctx context.Context, tenant, spuID int64, inputs []catalog.ProductSKUInput) error {
	if err := catalogQuery("Catalog.ReplaceManualSKUs"); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var spu catalog.ProductSPU
	if err := q.Select("id, source").Where("tenant_id=? AND id=? AND deleted_at IS NULL", tenant, spuID).Take(&spu).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return productNotFound()
	} else if err != nil {
		return err
	} else if spu.Source != "manual" {
		return productConflict("仅手工商品允许编辑 SKU")
	}
	var existing []catalog.ProductSKU
	if err := q.Select("id, code").Where("tenant_id=? AND spu_id=?", tenant, spuID).Find(&existing).Error; err != nil {
		return err
	}
	byCode := make(map[string]catalog.ProductSKU, len(existing))
	for _, row := range existing {
		if row.Code != nil {
			byCode[*row.Code] = row
		}
	}
	kept := make([]int64, 0, len(inputs))
	for _, input := range inputs {
		row := skuFromInput(tenant, spuID, input)
		if input.Code != nil {
			if old, ok := byCode[*input.Code]; ok {
				if err := q.Model(&catalog.ProductSKU{}).Where("tenant_id=? AND spu_id=? AND id=?", tenant, spuID, old.ID).Updates(map[string]any{"barcode": row.Barcode, "title": row.Title,
					"price_cents": row.PriceCents, "cost_cents": row.CostCents, "currency": row.Currency, "attributes": row.Attributes,
					"status": row.Status, "deleted_at": nil, "updated_at": time.Now().UTC()}).Error; err != nil {
					return err
				}
				kept = append(kept, old.ID)
				continue
			}
		}
		if err := q.Create(&row).Error; err != nil {
			return err
		}
		kept = append(kept, row.ID)
	}
	removed := q.Model(&catalog.ProductSKU{}).Where("tenant_id=? AND spu_id=?", tenant, spuID)
	if len(kept) > 0 {
		removed = removed.Where("id NOT IN ?", kept)
		if err := q.Model(&catalog.ListingSKU{}).Where("tenant_id=? AND sku_id IN (SELECT id FROM product_sku WHERE tenant_id=? AND spu_id=? AND id NOT IN ?)", tenant, tenant, spuID, kept).Update("sku_id", nil).Error; err != nil {
			return err
		}
	} else if err := q.Model(&catalog.ListingSKU{}).Where("tenant_id=? AND sku_id IN (SELECT id FROM product_sku WHERE tenant_id=? AND spu_id=?)", tenant, tenant, spuID).Update("sku_id", nil).Error; err != nil {
		return err
	}
	if err := removed.Updates(map[string]any{"deleted_at": time.Now().UTC(), "updated_at": time.Now().UTC()}).Error; err != nil {
		return err
	}
	return nil
}

func (s *productStore) MergeListings(ctx context.Context, tenant, spuID int64, listingIDs []int64) error {
	if err := catalogQuery("Catalog.MergeListings"); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var spu catalog.ProductSPU
	if err := q.Select("id").Where("tenant_id=? AND id=? AND deleted_at IS NULL", tenant, spuID).Take(&spu).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return productNotFound()
	} else if err != nil {
		return err
	}
	for _, listingID := range listingIDs {
		var listing catalog.Listing
		if err := q.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id, tenant_id, spu_id").Where("tenant_id=? AND id=?", tenant, listingID).Take(&listing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return productNotFound()
		} else if err != nil {
			return err
		}
		if listing.SPUID != nil && *listing.SPUID != spuID {
			return productConflict(fmt.Sprintf("listing %d 已归并到其他商品", listingID))
		}
		if err := q.Model(&catalog.Listing{}).Where("tenant_id=? AND id=?", tenant, listingID).Update("spu_id", spuID).Error; err != nil {
			return err
		}
		if err := bindListingSKUs(q, tenant, spuID, listingID); err != nil {
			return err
		}
	}
	return nil
}

func bindListingSKUs(q *gorm.DB, tenant, spuID, listingID int64) error {
	var rows []struct {
		catalog.ListingSKU
		Currency string `gorm:"column:currency"`
	}
	if err := q.Table("listing_sku ls").Select("ls.id, ls.sku_id, ls.external_sku_id, ls.title, ls.price_cents, l.currency").
		Joins("JOIN listing l ON l.tenant_id=ls.tenant_id AND l.id=ls.listing_id").
		Where("ls.tenant_id=? AND ls.listing_id=?", tenant, listingID).Order("ls.id").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if row.SKUID != nil {
			var owner int64
			if err := q.Model(&catalog.ProductSKU{}).Select("spu_id").Where("tenant_id=? AND id=? AND deleted_at IS NULL", tenant, *row.SKUID).Take(&owner).Error; err != nil {
				return err
			} else if owner != spuID {
				return productConflict("listing SKU 已绑定到其他商品")
			}
			continue
		}
		if len(row.Currency) != 3 {
			return errors.New("listing currency is invalid")
		}
		sku := catalog.ProductSKU{TenantID: tenant, SPUID: spuID, Title: row.Title, PriceCents: row.PriceCents, Currency: row.Currency, Status: "active", Attributes: []byte(`{}`)}
		if err := q.Create(&sku).Error; err != nil {
			return err
		}
		if err := q.Model(&catalog.ListingSKU{}).Where("tenant_id=? AND id=? AND sku_id IS NULL", tenant, row.ID).Update("sku_id", sku.ID).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *productStore) UnmergeListing(ctx context.Context, tenant, spuID, listingID int64) error {
	if err := catalogQuery("Catalog.UnmergeListing"); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Model(&catalog.Listing{}).Where("tenant_id=? AND id=? AND spu_id=?", tenant, listingID, spuID).Update("spu_id", nil)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return productNotFound()
	}
	return q.Model(&catalog.ListingSKU{}).Where("tenant_id=? AND listing_id=? AND sku_id IN (SELECT id FROM product_sku WHERE tenant_id=? AND spu_id=?)", tenant, listingID, tenant, spuID).Update("sku_id", nil).Error
}

func (s *productStore) RecomputeSPUSummary(ctx context.Context, tenant, spuID int64) error {
	if err := catalogQuery("Catalog.RecomputeSPUSummary"); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if err := catalog.RecomputeSPUSummary(ctx, q, tenant, spuID); err != nil {
		return err
	}
	var count int64
	if err := q.Model(&catalog.ProductSPU{}).Where("tenant_id=? AND id=? AND deleted_at IS NULL", tenant, spuID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return productNotFound()
	}
	return nil
}

func skuFromInput(tenant, spuID int64, input catalog.ProductSKUInput) catalog.ProductSKU {
	code := input.Code
	if code != nil {
		trimmed := strings.TrimSpace(*code)
		code = &trimmed
	}
	status := input.Status
	if status == "" {
		status = "active"
	}
	return catalog.ProductSKU{TenantID: tenant, SPUID: spuID, Code: code, Barcode: input.Barcode, Title: input.Title, PriceCents: input.PriceCents,
		CostCents: input.CostCents, Currency: input.Currency, Attributes: catalogJSON(input.Attributes), Status: status}
}

func catalogJSON(raw []byte) []byte {
	if len(raw) == 0 || string(raw) == "null" {
		return []byte(`{}`)
	}
	return raw
}

func productNotFound() error { return errs.New(40101, http.StatusNotFound, "商品不存在") }

func productConflict(message string) error {
	return errs.New(errs.ErrConflict, http.StatusConflict, message)
}

func productVersionConflict() error {
	return errs.New(errs.ErrVersionConflict, http.StatusConflict, "商品版本冲突")
}

package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/share"
	"gorm.io/gorm"
)

const priceListColumns = "p.id,p.tenant_id,p.type,p.customer_id,p.name,p.currency,p.status,p.valid_from,p.valid_to,p.version,p.revision,p.created_by,p.created_at,p.updated_at"

func (s *shareStore) PriceLists(ctx context.Context, tenant int64, in share.Query) ([]share.PriceList, int64, error) {
	base, cancel, err := s.base(ctx, tenant, "price_list", "Trade.PriceLists")
	if err != nil {
		return nil, 0, err
	}
	defer cancel()
	base = shareFilter(base, "p", in)
	if in.CustomerID > 0 {
		base = base.Where("p.customer_id=?", in.CustomerID)
	}
	if in.Type != "" {
		base = base.Where("p.type=?", in.Type)
	}
	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []share.PriceList{}
	err = base.Select(priceListColumns).Order(shareOrder("p", in.Sort)).Offset((in.Page - 1) * in.PageSize).Limit(in.PageSize).Find(&rows).Error
	return rows, total, shareDBError(err)
}

func (s *shareStore) PriceList(ctx context.Context, tenant, id int64, lock bool) (share.PriceList, error) {
	q, cancel, err := s.base(ctx, tenant, "price_list", "Trade.PriceList")
	if err != nil {
		return share.PriceList{}, err
	}
	defer cancel()
	var row share.PriceList
	err = shareLock(q, "p", lock).Select(priceListColumns).Where("p.id=?", id).Take(&row).Error
	return row, shareDBError(err)
}

func (s *shareStore) SavePriceList(ctx context.Context, row share.PriceList, create bool) error {
	if row.CustomerID != nil {
		if _, err := s.Customer(ctx, row.TenantID, *row.CustomerID, false); err != nil {
			return err
		}
	} else if err := s.ValidateOwner(ctx, row.TenantID, row.CreatedBy); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if create {
		return shareDBError(q.Create(&row).Error)
	}
	if _, err := s.PriceList(ctx, row.TenantID, row.ID, true); err != nil {
		return err
	}
	var mismatched int64
	err := q.Table("price_list_item i").Joins("JOIN product_sku k ON k.tenant_id=i.tenant_id AND k.id=i.sku_id").
		Where("i.tenant_id=? AND i.price_list_id=? AND k.currency<>?", row.TenantID, row.ID, row.Currency).Count(&mismatched).Error
	if err != nil {
		return err
	}
	if mismatched > 0 {
		return share.Invalid()
	}
	if row.Status != "active" {
		if err := s.revokeParentLinks(ctx, row.TenantID, row.ID, "price_list", "price_list_disabled"); err != nil {
			return err
		}
	}
	return shareChanged(q.Model(&share.PriceList{}).Where("tenant_id=? AND id=? AND deleted_at IS NULL AND version=?", row.TenantID, row.ID, row.Version-1).
		Updates(map[string]any{"type": row.Type, "customer_id": row.CustomerID, "name": row.Name, "currency": row.Currency, "status": row.Status, "valid_from": row.ValidFrom,
			"valid_to": row.ValidTo, "version": row.Version, "revision": row.Revision, "note_cipher": row.Note.Cipher, "note_nonce": row.Note.Nonce, "key_version": row.KeyVersion, "updated_at": time.Now().UTC()}))
}

func (s *shareStore) DeletePriceList(ctx context.Context, tenant, id int64, version int) error {
	row, err := s.PriceList(ctx, tenant, id, true)
	if err != nil {
		return err
	}
	if version <= 0 || row.Version != version {
		return share.VersionConflict()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if err := s.revokeParentLinks(ctx, tenant, id, "price_list", "price_list_deleted"); err != nil {
		return err
	}
	return shareChanged(q.Model(&share.PriceList{}).Where("tenant_id=? AND id=? AND deleted_at IS NULL AND version=?", tenant, id, version).
		Updates(map[string]any{"deleted_at": time.Now().UTC(), "updated_at": time.Now().UTC(), "status": "disabled", "version": version + 1}))
}

func (s *shareStore) BumpList(ctx context.Context, tenant, id int64, version int) error {
	if _, err := s.PriceList(ctx, tenant, id, true); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return shareChanged(q.Model(&share.PriceList{}).Where("tenant_id=? AND id=? AND deleted_at IS NULL AND version=?", tenant, id, version).
		Updates(map[string]any{"version": version + 1, "updated_at": time.Now().UTC()}))
}

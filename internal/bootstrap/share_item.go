package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/share"
)

func (s *shareStore) Items(ctx context.Context, tenant, list int64) ([]share.Item, error) {
	if _, err := s.PriceList(ctx, tenant, list, false); err != nil {
		return nil, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []share.Item{}
	err := q.Select("id,tenant_id,price_list_id,spu_id,sku_id,price_cents,moq,revision,created_at,updated_at").
		Where("tenant_id=? AND price_list_id=?", tenant, list).Order("id").Find(&rows).Error
	return rows, err
}

func (s *shareStore) SaveItem(ctx context.Context, row share.Item, create bool) error {
	if _, err := s.PriceList(ctx, row.TenantID, row.PriceListID, true); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if create {
		return shareDBError(q.Create(&row).Error)
	}
	return shareChanged(q.Model(&share.Item{}).Where("tenant_id=? AND price_list_id=? AND id=? AND revision=?", row.TenantID, row.PriceListID, row.ID, row.Revision-1).
		Updates(map[string]any{"price_cents": row.PriceCents, "moq": row.MOQ, "note_cipher": row.Note.Cipher, "note_nonce": row.Note.Nonce,
			"key_version": row.KeyVersion, "revision": row.Revision, "updated_at": time.Now().UTC()}))
}

func (s *shareStore) DeleteItem(ctx context.Context, tenant, list int64, in share.ItemInput) error {
	if _, err := s.PriceList(ctx, tenant, list, true); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Where("tenant_id=? AND price_list_id=? AND spu_id=? AND sku_id=?", tenant, list, in.SPUID, in.SKUID).Delete(&share.Item{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return share.NotFound()
	}
	return nil
}

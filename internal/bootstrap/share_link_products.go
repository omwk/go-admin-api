package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/share"
)

func (s *shareStore) PublicationProducts(ctx context.Context, tenant int64, members []share.Member) ([]share.PublicationProduct, error) {
	if err := s.ValidateOwner(ctx, tenant, shareActorUser(ctx)); err != nil {
		return nil, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	out := []share.PublicationProduct{}
	for _, member := range members {
		base := q.Table("product_spu p").Joins("JOIN product_sku k ON k.tenant_id=p.tenant_id AND k.spu_id=p.id").
			Where("p.tenant_id=? AND p.id=? AND p.status='active' AND p.deleted_at IS NULL AND k.status='active' AND k.deleted_at IS NULL", tenant, member.SPUID)
		if len(member.SKUIDs) > 0 {
			base = base.Where("k.id IN ?", member.SKUIDs)
		}
		rows := []share.PublicationProduct{}
		err := base.Select("p.id AS spu_id,k.id AS sku_id,p.title,k.title AS sku_title,k.price_cents AS original_price,k.currency,CASE WHEN k.stock_cached>100 THEN '充足' WHEN k.stock_cached>0 THEN '紧张' ELSE '缺货' END AS stock_display").
			Order("k.id").Limit(501).Find(&rows).Error
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 || len(member.SKUIDs) > 0 && len(rows) != len(member.SKUIDs) {
			return nil, share.NotFound()
		}
		out = append(out, rows...)
		if len(out) > 500 {
			return nil, share.Invalid()
		}
	}
	return out, nil
}

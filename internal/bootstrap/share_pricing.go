package bootstrap

import (
	"context"

	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/omwk/go-admin-api/internal/modules/share/pricing"
)

func (s *shareStore) Product(ctx context.Context, tenant, spu, sku int64) (share.ProductPrice, error) {
	a, err := identityscope.From(ctx)
	if err != nil || a.Tenant != tenant {
		return share.ProductPrice{}, identityscope.Denied()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	base := q.Table("product_spu p").Where("p.tenant_id=? AND p.id=? AND p.deleted_at IS NULL AND p.status='active'", tenant, spu)
	var row share.ProductPrice
	if sku > 0 {
		err = base.Joins("JOIN product_sku k ON k.tenant_id=p.tenant_id AND k.spu_id=p.id").
			Where("k.id=? AND k.deleted_at IS NULL AND k.status='active'", sku).
			Select("p.id AS spu_id,k.id AS sku_id,k.price_cents,k.currency").Take(&row).Error
	} else {
		err = base.Select("p.id AS spu_id").Take(&row).Error
	}
	return row, shareDBError(err)
}

func (s *shareStore) Candidates(ctx context.Context, tenant, customer int64, spus []int64) ([]pricing.Candidate, error) {
	if _, err := scopeRegistryQuery("Trade.Candidates"); err != nil {
		return nil, err
	}
	if _, err := s.Customer(ctx, tenant, customer, false); err != nil {
		return nil, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []pricing.Candidate{}
	err := q.Table("price_list p").Joins("JOIN price_list_item i ON i.tenant_id=p.tenant_id AND i.price_list_id=p.id").
		Where("p.tenant_id=? AND p.deleted_at IS NULL AND p.status='active' AND i.spu_id IN ? AND ((p.type='customer' AND p.customer_id=?) OR (p.type='default' AND p.customer_id IS NULL))", tenant, spus, customer).
		Select("p.tenant_id,p.customer_id,p.id AS price_list_id,i.id AS item_id,i.spu_id,i.sku_id,p.version,p.type,p.status,p.currency,p.valid_from,p.valid_to,i.price_cents,i.moq").
		Order("p.id,i.id").Find(&rows).Error
	return rows, err
}

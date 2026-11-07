package bootstrap

import (
	"github.com/omwk/go-admin-api/internal/modules/order"
	"gorm.io/gorm"
)

const orderDisplaySQL = `CASE WHEN o.payment_status='refunded' AND o.after_sales_status='refunded' THEN 'refunded' WHEN o.after_sales_status='refunding' THEN 'refunding' WHEN o.lifecycle_status='paid' THEN 'pending_shipment' WHEN o.lifecycle_status='cancelled' THEN 'closed' WHEN o.lifecycle_status IN ('pending_payment','shipped','completed') THEN o.lifecycle_status ELSE 'unknown' END`

func orderQueryFilter(q *gorm.DB, query order.Query, stats bool) *gorm.DB {
	if !stats && !query.IncludeVanished {
		q = q.Where("o.vanished_at IS NULL")
	}
	if query.ShopID > 0 {
		q = q.Where("o.shop_id=?", query.ShopID)
	}
	if query.Status != "" {
		q = q.Where("("+orderDisplaySQL+")=?", query.Status)
	}
	if query.CreatedFrom != nil {
		q = q.Where("o.created_at>=?", query.CreatedFrom)
	}
	if query.CreatedTo != nil {
		q = q.Where("o.created_at<?", query.CreatedTo)
	}
	if query.MinAmountCents != nil {
		q = q.Where("o.total_amount_cents>=?", query.MinAmountCents)
	}
	if query.MaxAmountCents != nil {
		q = q.Where("o.total_amount_cents<=?", query.MaxAmountCents)
	}
	return q
}

package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/order"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"gorm.io/gorm"
)

func (s *exportStore) CreateOrderExport(ctx context.Context, tenant, user int64, input order.ExportRequest, op *idem.Operation) (job catalog.ExportJob, err error) {
	if _, err := scopeRegistryQuery("Order.ExportCreate"); err != nil {
		return job, err
	}
	ctx, err = (&orderQueryStore{database: s.database}).Authorize(ctx, tenant, user, permission.TradeOrderExport)
	if err != nil {
		return job, err
	}
	err = s.database.Transaction(ctx, func(ctx context.Context) error {
		shops, err := s.orderExportShops(ctx, tenant)
		if err != nil {
			return err
		}
		if input.Filters.ShopID > 0 {
			if !slices.Contains(shops, input.Filters.ShopID) {
				return order.NotFound()
			}
			shops = []int64{input.Filters.ShopID}
		}
		raw, err := json.Marshal(order.ExportSnapshot{ExportRequest: input, FieldsVersion: 1, ShopIDs: shops})
		if err != nil {
			return errors.New("encode order export snapshot")
		}
		job, err = s.createExportJob(ctx, tenant, user, "orders", raw, op)
		return err
	})
	return job, err
}

func (s *exportStore) orderExportShops(ctx context.Context, tenant int64) ([]int64, error) {
	if _, err := scopeRegistryQuery("Order.ExportScope"); err != nil {
		return nil, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	scoped, err := shopScope(ctx, shopmodel.Shop{}, q.Table("shop"))
	if err != nil {
		return nil, err
	}
	var shops []int64
	err = scoped.Select("shop.id").Where("shop.tenant_id=? AND shop.status='active'", tenant).Order("shop.id").Find(&shops).Error
	return shops, err
}

func (s *exportStore) CheckOrderExport(ctx context.Context, tenant, user, id int64) error {
	job, err := s.ReadExport(ctx, tenant, user, id)
	if err != nil {
		return err
	}
	if job.Kind != "orders" {
		return catalog.ExportNotFound()
	}
	return s.checkOrderExportScope(ctx, job)
}

func (s *exportStore) checkOrderExportScope(ctx context.Context, job catalog.ExportJob) error {
	ctx, err := (&orderQueryStore{database: s.database}).Authorize(ctx, job.TenantID, job.RequestedBy, permission.TradeOrderExport)
	if err != nil {
		return err
	}
	var snapshot order.ExportSnapshot
	if json.Unmarshal(job.Filters, &snapshot) != nil || snapshot.FieldsVersion != 1 {
		return errors.New("invalid order export snapshot")
	}
	shops, err := s.orderExportShops(ctx, job.TenantID)
	if err != nil {
		return err
	}
	for _, id := range snapshot.ShopIDs {
		if !slices.Contains(shops, id) {
			return errs.New(errs.ErrDataScopeDenied, 403, "超出数据权限")
		}
	}
	return nil
}

func (s *exportStore) materializeOrderExport(ctx context.Context, q *gorm.DB, job catalog.ExportJob) (int64, error) {
	if _, err := scopeRegistryQuery("Order.ExportMaterialize"); err != nil {
		return 0, err
	}
	if err := s.checkOrderExportScope(ctx, job); err != nil {
		return 0, err
	}
	var snapshot order.ExportSnapshot
	if json.Unmarshal(job.Filters, &snapshot) != nil || snapshot.FieldsVersion != 1 {
		return 0, errors.New("invalid order export snapshot")
	}
	base := q.Session(&gorm.Session{NewDB: true}).Table("sales_order o").Where("o.tenant_id=? AND o.deleted_at IS NULL", job.TenantID)
	if len(snapshot.ShopIDs) == 0 {
		base = base.Where("false")
	} else {
		base = base.Where("o.shop_id IN ?", snapshot.ShopIDs)
	}
	base = orderQueryFilter(base, snapshot.Filters.Query(), false)
	projection := `jsonb_build_object('id',o.id,'shop_id',o.shop_id,'order_no',o.order_no,'status',` + orderDisplaySQL + `,
'currency',o.currency,'goods_amount_cents',o.goods_amount_cents,'shipping_fee_cents',o.shipping_fee_cents,
'discount_cents',o.discount_cents,'total_amount_cents',o.total_amount_cents,'paid_amount_cents',o.paid_amount_cents,
'refunded_amount_cents',o.refunded_amount_cents,'item_count',o.item_count,'placed_at',o.placed_at,'created_at',o.created_at,'updated_at',o.updated_at)`
	statement := base.Select("?, ?, row_number() OVER (ORDER BY o.created_at DESC,o.id DESC), "+projection+", jsonb_build_object('id',o.id,'created_at',o.created_at), statement_timestamp()", job.ID, job.Attempt).
		Session(&gorm.Session{DryRun: true}).Find(&[]catalog.ExportRow{}).Statement
	result := q.Exec("INSERT INTO export_job_row(export_id,attempt,ordinal,projection,sort_key,generated_at) "+statement.SQL.String(), statement.Vars...)
	return result.RowsAffected, result.Error
}

var _ order.ExportStore = (*exportStore)(nil)

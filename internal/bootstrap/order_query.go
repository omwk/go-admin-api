package bootstrap

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/order"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"gorm.io/gorm"
)

type orderQueryStore struct{ database *db.DB }

func registerTradeRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, cfg *config.Config) error {
	if err := registerExportRoutes(router, auth, profiles, database, cfg); err != nil {
		return err
	}
	queries, err := order.NewQueryService(NewOrderQueryStore(database))
	if err != nil {
		return err
	}
	sealer, err := credentialSealer(cfg)
	if err != nil {
		return err
	}
	pii, err := order.NewPIIService(NewOrderPIIStore(database), sealer, auth.RequireRecent)
	if err != nil {
		return err
	}
	if err := registerOrderExportRoutes(router, auth, profiles, database, cfg); err != nil {
		return err
	}
	return order.RegisterRoutes(router, auth, profiles, queries, pii)
}

// NewOrderQueryStore constructs the live shop-scoped order projection adapter.
func NewOrderQueryStore(database *db.DB) order.QueryStore {
	return &orderQueryStore{database: database}
}

func (s *orderQueryStore) Authorize(ctx context.Context, tenant, user int64, code string) (context.Context, error) {
	profiles, err := identity.NewProfileService(newProfileStore(s.database))
	if err != nil {
		return ctx, err
	}
	actor, err := profiles.Me(ctx, tenant, user)
	if err != nil {
		return ctx, err
	}
	allowed := actor.User.IsSuper
	for _, candidate := range actor.Permissions {
		allowed = allowed || candidate == code
	}
	if !allowed {
		return ctx, errs.New(errs.ErrPermissionDenied, 403, "权限不足")
	}
	return newAdminStore(s.database).Authorize(ctx, tenant, user)
}

func (s *orderQueryStore) Snapshot(ctx context.Context, fn func(context.Context) error) error {
	return s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		if err := q.Exec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY").Error; err != nil {
			return err
		}
		return fn(ctx)
	})
}

func (s *orderQueryStore) base(ctx context.Context, tenant int64, name string) (*gorm.DB, context.CancelFunc, error) {
	if _, err := scopeRegistryQuery(name); err != nil {
		return nil, func() {}, err
	}
	human, err := identityscope.From(ctx)
	if err != nil || human.Tenant != tenant {
		return nil, func() {}, identityscope.Denied()
	}
	q, cancel := s.database.WithContext(ctx)
	scoped, err := shopScope(ctx, shopmodel.Shop{}, q.Table("shop"))
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	return scoped.Joins("JOIN sales_order o ON o.tenant_id=shop.tenant_id AND o.shop_id=shop.id").Where("o.deleted_at IS NULL"), cancel, nil
}

const orderViewColumns = "o.id,o.shop_id,o.platform_id,o.external_order_id,o.order_no,o.lifecycle_status,o.fulfillment_status,o.payment_status,o.after_sales_status,o.cancel_status,o.platform_status,o.currency,o.goods_amount_cents,o.shipping_fee_cents,o.discount_cents,o.total_amount_cents,o.paid_amount_cents,o.refunded_amount_cents,o.formula_delta_cents,o.payment_delta_cents,o.item_count,o.placed_at,o.created_at,o.updated_at,o.vanished_at,o.vanish_reason"

func (s *orderQueryStore) List(ctx context.Context, tenant int64, query order.Query) (order.Page, error) {
	base, cancel, err := s.base(ctx, tenant, "Order.List")
	if err != nil {
		return order.Page{}, err
	}
	defer cancel()
	out := order.Page{List: []order.View{}, Page: query.Page, PageSize: query.PageSize}
	base = orderQueryFilter(base, query, false)
	if err := base.Session(&gorm.Session{}).Count(&out.Total).Error; err != nil {
		return out, err
	}
	rows := []order.Order{}
	err = base.Select(orderViewColumns).Order("o.created_at DESC,o.id DESC").Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&rows).Error
	for _, row := range rows {
		out.List = append(out.List, safeOrderView(row))
	}
	if err == nil {
		ids := make([]int64, len(out.List))
		for i := range out.List {
			ids[i] = out.List[i].ID
		}
		masks, maskErr := s.maskedAddresses(ctx, tenant, ids)
		if maskErr != nil {
			return order.Page{}, maskErr
		}
		for i := range out.List {
			if mask, ok := masks[out.List[i].ID]; ok {
				out.List[i].Receiver = mask
			}
		}
	}
	return out, err
}

func (s *orderQueryStore) Get(ctx context.Context, tenant, id int64) (order.Detail, error) {
	base, cancel, err := s.base(ctx, tenant, "Order.Get")
	if err != nil {
		return order.Detail{}, err
	}
	defer cancel()
	var row order.Order
	err = base.Select(orderViewColumns+",o.raw_id,o.source_fetched_at,o.mapping_checksum").Where("o.id=?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return order.Detail{}, order.NotFound()
	}
	if err != nil {
		return order.Detail{}, err
	}
	out := order.Detail{View: safeOrderView(row), Raw: order.Source{RawID: row.RawID, FetchedAt: row.SourceFetchedAt, MappingChecksum: row.MappingChecksum}, Items: []order.ItemView{}, History: []order.HistoryView{}}
	q, stop := s.database.WithContext(ctx)
	defer stop()
	err = q.Table("order_item").Select("id,external_item_id,title,sku_title,currency,price_cents,qty,discount_cents,amount_cents,order_discount_cents,allocation_source,refunded_amount_cents,refund_status").Where("tenant_id=? AND order_id=? AND deleted_at IS NULL", tenant, id).Order("id").Find(&out.Items).Error
	if err != nil {
		return order.Detail{}, err
	}
	var history []struct {
		order.HistoryView `gorm:"embedded"`
		Evidence          json.RawMessage
	}
	err = q.Table("order_status_history").Select("id,dimension,from_status,to_status,event_kind,reason,occurred_at,observed_at,evidence").Where("tenant_id=? AND order_id=?", tenant, id).Order("observed_at,id").Find(&history).Error
	if err != nil {
		return order.Detail{}, err
	}
	for _, row := range history {
		var evidence struct {
			Source *order.Source `json:"source"`
		}
		if err := json.Unmarshal(row.Evidence, &evidence); err != nil {
			return order.Detail{}, err
		}
		row.HistoryView.Source = evidence.Source
		out.History = append(out.History, row.HistoryView)
	}
	masks, err := s.maskedAddresses(ctx, tenant, []int64{id})
	if err != nil {
		return order.Detail{}, err
	}
	if mask, ok := masks[id]; ok {
		out.Receiver = mask
	}
	return out, nil
}

func (s *orderQueryStore) Stats(ctx context.Context, tenant int64, query order.Query) ([]order.Stat, error) {
	base, cancel, err := s.base(ctx, tenant, "Order.Stats")
	if err != nil {
		return nil, err
	}
	defer cancel()
	rows := []order.Stat{}
	expression := "to_char(COALESCE(o.placed_at,o.created_at) AT TIME ZONE 'UTC','YYYY-MM-DD')"
	err = orderQueryFilter(base, query, true).Select(expression + " AS date," + orderDisplaySQL + " AS status,o.currency,count(*) AS order_count,SUM(o.total_amount_cents) AS total_amount_cents,SUM(o.paid_amount_cents) AS paid_amount_cents,SUM(o.refunded_amount_cents) AS refunded_amount_cents").Group(expression + "," + orderDisplaySQL + ",o.currency").Order("date,status,o.currency").Find(&rows).Error
	return rows, err
}

func safeOrderView(row order.Order) order.View {
	return order.View{
		Receiver: order.MaskedAddress("", ""),
		ID:       row.ID, ShopID: row.ShopID, PlatformID: row.PlatformID,
		ExternalOrderID: row.ExternalOrderID, OrderNo: row.OrderNo, Status: row.States.DisplayStatus(),
		LifecycleStatus: row.Lifecycle, FulfillmentStatus: row.Fulfillment, PaymentStatus: row.Payment,
		AfterSalesStatus: row.AfterSales, CancelStatus: row.Cancel, PlatformStatus: row.PlatformStatus,
		Currency: row.Currency, GoodsAmountCents: row.GoodsAmountCents, ShippingFeeCents: row.ShippingFeeCents,
		DiscountCents: row.DiscountCents, TotalAmountCents: row.TotalAmountCents, PaidAmountCents: row.PaidAmountCents,
		RefundedAmountCents: row.RefundedAmountCents, FormulaDeltaCents: row.FormulaDeltaCents, PaymentDeltaCents: row.PaymentDeltaCents,
		ItemCount: row.ItemCount, PlacedAt: row.PlacedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		VanishedAt: row.VanishedAt, VanishReason: row.VanishReason,
	}
}

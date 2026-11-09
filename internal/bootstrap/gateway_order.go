package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/order"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"gorm.io/gorm"
)

type gatewayOrderReader struct {
	service *order.ApplicationStatusService
}
type gatewayOrderStore struct{ database *db.DB }

// NewGatewayOrderReader constructs the application-purpose order status port.
func NewGatewayOrderReader(database *db.DB) (gateway.OrderStatusReader, error) {
	if database == nil {
		return nil, errors.New("gateway order database required")
	}
	svc, err := order.NewApplicationStatusService(&gatewayOrderStore{database: database})
	return &gatewayOrderReader{service: svc}, err
}

func (s *gatewayOrderReader) OrderStatus(ctx context.Context, p gateway.Principal, id string) (gateway.OpenOrderStatus, error) {
	out, err := s.service.Get(ctx, order.ApplicationStatusRequest{TenantID: p.TenantID, ClientID: p.ClientID, OwnerUserID: p.OwnerUserID, SecretID: p.SecretID, AppID: p.AppID, PublicID: id})
	return gateway.OpenOrderStatus{ID: out.ID, OrderNo: out.OrderNo, Status: out.Status, UpdatedAt: out.UpdatedAt.UTC()}, err
}

func (s *gatewayOrderStore) ReadApplicationStatus(ctx context.Context, in order.ApplicationStatusRequest) (order.ApplicationStatus, error) {
	scoped, err := (&orderQueryStore{database: s.database}).Authorize(ctx, in.TenantID, in.OwnerUserID, permission.TradeOrderRead)
	if err != nil {
		return order.ApplicationStatus{}, gatewayOwnerError(err)
	}
	q, cancel := s.database.WithContext(scoped)
	defer cancel()
	q, err = shopScope(scoped, shopmodel.Shop{}, q.Table("shop"))
	if err != nil {
		return order.ApplicationStatus{}, gatewayOwnerError(err)
	}
	var row struct {
		OrderNo                                                                           string
		UpdatedAt                                                                         time.Time
		LifecycleStatus, FulfillmentStatus, PaymentStatus, AfterSalesStatus, CancelStatus string
	}
	err = q.Select("o.order_no,o.updated_at,o.lifecycle_status,o.fulfillment_status,o.payment_status,o.after_sales_status,o.cancel_status").
		Joins("JOIN sales_order o ON o.tenant_id=shop.tenant_id AND o.shop_id=shop.id AND o.deleted_at IS NULL").
		Joins("JOIN api_client_resource_grant g ON g.tenant_id=o.tenant_id AND g.resource_type='order' AND g.resource_id=o.id").
		Joins("JOIN api_client c ON c.tenant_id=g.tenant_id AND c.id=g.client_id").
		Joins("JOIN tenant t ON t.id=c.tenant_id AND t.status='active'").
		Joins("JOIN api_client_secret k ON k.tenant_id=c.tenant_id AND k.client_id=c.id AND k.id=?", in.SecretID).
		Where("c.tenant_id=? AND c.id=? AND c.app_id=? AND c.owner_user_id=? AND c.status='active'", in.TenantID, in.ClientID, in.AppID, in.OwnerUserID).
		Where("g.public_id=? AND g.status='active' AND (g.expires_at IS NULL OR g.expires_at>now())", in.PublicID).
		Where("k.status='active' AND (k.expires_at IS NULL OR k.expires_at>now())").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return order.ApplicationStatus{}, gateway.ResourceUnavailable()
	}
	if err != nil {
		return order.ApplicationStatus{}, gatewayDBError(err)
	}
	states := order.States{Lifecycle: row.LifecycleStatus, Fulfillment: row.FulfillmentStatus, Payment: row.PaymentStatus, AfterSales: row.AfterSalesStatus, Cancel: row.CancelStatus}
	return order.ApplicationStatus{ID: in.PublicID, OrderNo: row.OrderNo, Status: states.DisplayStatus(), UpdatedAt: row.UpdatedAt.UTC()}, nil
}

func gatewayOwnerError(err error) error {
	if errs.From(err).HTTPStatus < 500 {
		return gateway.ResourceUnavailable()
	}
	return gateway.Unavailable()
}

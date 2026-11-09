package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// NewGatewayResourceStore supplies transaction-bound order ceilings.
func NewGatewayResourceStore(database *db.DB) gateway.ResourceStore {
	return &gatewayStore{database: database}
}

func (s *gatewayStore) GrantOrder(ctx context.Context, a gateway.Actor, g *gateway.ResourceGrant) error {
	client, err := s.LockClient(ctx, a.TenantID, g.ClientID)
	if err != nil {
		return err
	}
	if client.OwnerUserID != a.UserID || client.Status != "active" {
		return gateway.ResourceUnavailable()
	}
	if err := s.visibleOrder(ctx, a, g.ResourceID); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	// Regrant deliberately replaces the public identifier; old identifiers stay unusable.
	result := q.Table("api_client_resource_grant").Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_id"}, {Name: "resource_type"}, {Name: "resource_id"}},
		DoUpdates: clause.Assignments(map[string]any{"public_id": g.PublicID, "status": "active", "version": gorm.Expr("api_client_resource_grant.version+1"), "granted_by": a.UserID, "granted_at": q.NowFunc(), "expires_at": nil}),
	}).Create(g)
	if result.Error != nil {
		return gatewayDBError(result.Error)
	}
	return gatewayDBError(q.Table("api_client_resource_grant").Select("id,tenant_id,client_id,resource_type,resource_id,public_id,status,version,granted_by,granted_at,expires_at").Where("client_id=? AND public_id=?", g.ClientID, g.PublicID).Take(g).Error)
}

func (s *gatewayStore) visibleOrder(ctx context.Context, a gateway.Actor, id int64) error {
	scoped, err := (&orderQueryStore{database: s.database}).Authorize(ctx, a.TenantID, a.UserID, permission.TradeOrderRead)
	if err != nil {
		return gatewayOwnerError(err)
	}
	q, cancel := s.database.WithContext(scoped)
	defer cancel()
	q, err = shopScope(scoped, shopmodel.Shop{}, q.Table("shop"))
	if err != nil {
		return gatewayOwnerError(err)
	}
	var count int64
	err = q.Joins("JOIN sales_order o ON o.tenant_id=shop.tenant_id AND o.shop_id=shop.id").Where("o.tenant_id=? AND o.id=? AND o.deleted_at IS NULL", a.TenantID, id).Count(&count).Error
	if err != nil {
		return gatewayDBError(err)
	}
	if count != 1 {
		return gateway.ResourceUnavailable()
	}
	return nil
}

func (s *gatewayStore) RevokeOrder(ctx context.Context, a gateway.Actor, client, id int64, version int) error {
	row, err := s.LockClient(ctx, a.TenantID, client)
	if err != nil {
		return err
	}
	if row.OwnerUserID != a.UserID {
		return gateway.ResourceUnavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var current struct{ Version int }
	if err := q.Table("api_client_resource_grant").Select("version").Where("tenant_id=? AND client_id=? AND id=?", a.TenantID, client, id).Take(&current).Error; err != nil {
		return gatewayDBError(err)
	}
	if current.Version != version {
		return errs.New(10409, 409, "资源版本冲突")
	}
	return gatewayMutationError(q.Table("api_client_resource_grant").Where("tenant_id=? AND client_id=? AND id=? AND version=?", a.TenantID, client, id, version).Updates(map[string]any{"status": "suspended", "version": version + 1}))
}

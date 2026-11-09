package bootstrap

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const gatewayRouteColumns = "id,tenant_id,code,name,capability,method,path,route_group,visibility,status,billing_mode,version,created_by"

func (s *gatewayStore) Route(ctx context.Context, p gateway.Principal, code string) (gateway.Route, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var out gateway.Route
	err := q.Table("api_route").Select(gatewayRouteColumns).Where("tenant_id=? AND code=?", p.TenantID, code).Take(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, gateway.RouteUnavailable()
	}
	return out, gatewayDBError(err)
}

func (s *gatewayStore) RouteGranted(ctx context.Context, p gateway.Principal, route int64, now time.Time) (bool, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var count int64
	err := q.Table("api_client_route").Where("tenant_id=? AND client_id=? AND route_id=? AND status='active' AND (expires_at IS NULL OR expires_at>?)", p.TenantID, p.ClientID, route, now).Count(&count).Error
	return count == 1, gatewayDBError(err)
}

func (s *gatewayStore) ListClients(ctx context.Context, a gateway.Actor) ([]gateway.ClientInfo, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []gatewayClientRow{}
	err := q.Table("api_client c").Select(gatewayClientColumns).Where("c.tenant_id=? AND c.owner_user_id=?", a.TenantID, a.UserID).Order("c.id DESC").Limit(100).Find(&rows).Error
	out := make([]gateway.ClientInfo, 0, len(rows))
	for _, row := range rows {
		row.Client.IPWhitelist = []string(row.Networks)
		out = append(out, gateway.ClientView(row.Client))
	}
	return out, gatewayDBError(err)
}

func (s *gatewayStore) ListSecrets(ctx context.Context, a gateway.Actor, client int64) ([]gateway.SecretInfo, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var owned int64
	if err := q.Table("api_client").Where("tenant_id=? AND id=? AND owner_user_id=?", a.TenantID, client, a.UserID).Count(&owned).Error; err != nil {
		return nil, gatewayDBError(err)
	}
	if owned != 1 {
		return nil, gateway.ResourceUnavailable()
	}
	out := []gateway.SecretInfo{}
	err := q.Table("api_client_secret").Select("id,secret_prefix,status,expires_at,last_used_at").Where("tenant_id=? AND client_id=?", a.TenantID, client).Order("id DESC").Limit(100).Find(&out).Error
	return out, gatewayDBError(err)
}

func (s *gatewayStore) ListRoutes(ctx context.Context, a gateway.Actor) ([]gateway.Route, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	out := []gateway.Route{}
	err := q.Table("api_route").Select(gatewayRouteColumns).Where("tenant_id=?", a.TenantID).Order("id").Limit(100).Find(&out).Error
	return out, gatewayDBError(err)
}

func (s *gatewayStore) SaveRoute(ctx context.Context, a gateway.Actor, in gateway.Route) (gateway.Route, error) {
	err := s.Transaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		in.TenantID = a.TenantID
		if in.ID == 0 {
			in.CreatedBy = a.UserID
			in.Version = 1
			result := q.Table("api_route").Clauses(clause.OnConflict{DoNothing: true}).Create(&in)
			if result.Error != nil {
				return gatewayDBError(result.Error)
			}
			if result.RowsAffected != 1 {
				return errs.New(10004, 409, "路由已登记")
			}
		} else {
			var current gateway.Route
			if err := q.Table("api_route").Select(gatewayRouteColumns).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", a.TenantID, in.ID).Take(&current).Error; err != nil {
				return gatewayDBError(err)
			}
			if current.Version != in.Version {
				return errs.New(10409, 409, "资源版本冲突")
			}
			result := q.Table("api_route").Where("tenant_id=? AND id=? AND version=?", a.TenantID, in.ID, in.Version).Updates(map[string]any{"name": in.Name, "visibility": in.Visibility, "status": in.Status, "version": in.Version + 1, "updated_at": time.Now().UTC()})
			if err := gatewayMutationError(result); err != nil {
				return err
			}
			in.Version++
		}
		return s.Audit(tx, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: "gateway.route.save", ResourceType: "api_route", ResourceID: strconv.FormatInt(in.ID, 10), After: map[string]any{"code": in.Code, "status": in.Status, "version": in.Version}, Result: "success"})
	})
	return in, err
}

func (s *gatewayStore) GrantRoute(ctx context.Context, a gateway.Actor, client, route int64, status string, expires *time.Time, limits gateway.QuotaLimits) error {
	if limits.Daily < 0 || limits.Monthly < 0 || limits.Daily > 2147483647 || limits.Monthly > 2147483647 {
		return gateway.Invalid()
	}
	return s.Transaction(ctx, func(tx context.Context) error {
		c, err := s.LockClient(tx, a.TenantID, client)
		if err != nil {
			return err
		}
		if c.OwnerUserID != a.UserID {
			return gateway.ResourceUnavailable()
		}
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		var count int64
		if err := q.Table("api_route").Where("tenant_id=? AND id=?", a.TenantID, route).Count(&count).Error; err != nil {
			return gatewayDBError(err)
		}
		if count != 1 {
			return gateway.RouteUnavailable()
		}
		values := map[string]any{"tenant_id": a.TenantID, "client_id": client, "route_id": route, "status": status, "granted_by": a.UserID, "expires_at": expires, "quota_daily": limits.Daily, "quota_monthly": limits.Monthly}
		if err := q.Table("api_client_route").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "client_id"}, {Name: "route_id"}}, DoUpdates: clause.Assignments(map[string]any{"status": status, "expires_at": expires, "version": gorm.Expr("api_client_route.version+1"), "granted_by": a.UserID, "granted_at": time.Now().UTC(), "quota_daily": limits.Daily, "quota_monthly": limits.Monthly})}).Create(values).Error; err != nil {
			return gatewayDBError(err)
		}
		return s.Audit(tx, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: "gateway.route.grant", ResourceType: "api_client_route", ResourceID: strconv.FormatInt(client, 10) + ":" + strconv.FormatInt(route, 10), After: map[string]any{"client_id": client, "route_id": route, "status": status, "quota_daily": limits.Daily, "quota_monthly": limits.Monthly}, Result: "success"})
	})
}

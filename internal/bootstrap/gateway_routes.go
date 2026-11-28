package bootstrap

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/lib/pq"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const gatewayRouteColumns = "id,tenant_id,code,name,capability,method,path,route_group,visibility,status,billing_mode,version,created_by,price_cents,units_per_call,currency"

func (s *gatewayStore) Route(ctx context.Context, p gateway.Principal, code string) (gateway.Route, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	key := "route:" + strconv.FormatInt(p.TenantID, 10) + ":" + code
	var current struct {
		Version                                     int
		Status, Visibility, Capability, BillingMode string
	}
	if err := q.Table("api_route").Select("version,status,visibility,capability,billing_mode").Where("tenant_id=? AND code=?", p.TenantID, code).Take(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return gateway.Route{}, gateway.RouteUnavailable()
		}
		return gateway.Route{}, gatewayDBError(err)
	}
	version := strconv.Itoa(current.Version) + ":" + current.Status + ":" + current.Visibility + ":" + current.Capability + ":" + current.BillingMode
	if cached, ok := s.state.get(key, version); ok {
		return cached.(gateway.Route), nil
	}
	var out gateway.Route
	err := q.Table("api_route").Select(gatewayRouteColumns).Where("tenant_id=? AND code=?", p.TenantID, code).Take(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, gateway.RouteUnavailable()
	}
	if err == nil {
		s.state.put(key, version, out)
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

func (s *gatewayStore) GetClient(ctx context.Context, a gateway.Actor, client int64) (gateway.ClientInfo, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row gatewayClientRow
	err := q.Table("api_client c").Select(gatewayClientColumns).
		Where("c.tenant_id=? AND c.id=? AND c.owner_user_id=?", a.TenantID, client, a.UserID).Take(&row).Error
	if err != nil {
		return gateway.ClientInfo{}, gatewayDBError(err)
	}
	row.Client.IPWhitelist = []string(row.Networks)
	return gateway.ClientView(row.Client), nil
}

func (s *gatewayStore) UpdateClient(ctx context.Context, a gateway.Actor, id int64, update gateway.ClientUpdate, version int) (out gateway.ClientInfo, err error) {
	defer s.state.invalidate()
	err = s.Transaction(ctx, func(tx context.Context) error {
		before, lockErr := s.LockClient(tx, a.TenantID, id)
		if lockErr != nil {
			return lockErr
		}
		if before.OwnerUserID != a.UserID {
			return gateway.ResourceUnavailable()
		}
		if before.Version != version {
			return errs.New(10409, 409, "客户端已被修改，请刷新")
		}
		changes := map[string]any{"version": version + 1, "updated_at": time.Now().UTC()}
		beforeFields, afterFields := map[string]any{}, map[string]any{}
		if update.Name != nil && before.Name != *update.Name {
			changes["name"] = *update.Name
			beforeFields["name"], afterFields["name"] = before.Name, *update.Name
			before.Name = *update.Name
		}
		if update.IPWhitelist != nil && !slices.Equal(before.IPWhitelist, *update.IPWhitelist) {
			changes["ip_whitelist"] = pq.StringArray(*update.IPWhitelist)
			beforeFields["ip_whitelist"], afterFields["ip_whitelist"] = before.IPWhitelist, *update.IPWhitelist
			before.IPWhitelist = append([]string{}, *update.IPWhitelist...)
		}
		if update.Status != nil && before.Status != *update.Status {
			changes["status"] = *update.Status
			beforeFields["status"], afterFields["status"] = before.Status, *update.Status
			before.Status = *update.Status
		}
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		result := q.Table("api_client").Where("tenant_id=? AND owner_user_id=? AND id=? AND version=?", a.TenantID, a.UserID, id, version).Updates(changes)
		if result.Error != nil {
			return gatewayDBError(result.Error)
		}
		if result.RowsAffected != 1 {
			return gateway.ResourceUnavailable()
		}
		if len(beforeFields) > 0 {
			if err := s.Audit(tx, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: "gateway.client.update", ResourceType: "api_client", ResourceID: strconv.FormatInt(id, 10), Before: beforeFields, After: afterFields, Result: "success"}); err != nil {
				return err
			}
		}
		out = gateway.ClientView(before)
		out.Version = version + 1
		return nil
	})
	return out, err
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

func (s *gatewayStore) ListGrants(ctx context.Context, a gateway.Actor, client int64) ([]gateway.RouteGrantInfo, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var owned int64
	if err := q.Table("api_client").Where("tenant_id=? AND id=? AND owner_user_id=?", a.TenantID, client, a.UserID).Count(&owned).Error; err != nil {
		return nil, gatewayDBError(err)
	}
	if owned != 1 {
		return nil, gateway.ResourceUnavailable()
	}
	out := []gateway.RouteGrantInfo{}
	err := q.Table("api_client_route g").Select(`g.id,g.route_id,
		COALESCE(r.code, '#' || g.route_id::text) AS route_code,COALESCE(r.name, '') AS route_name,
		COALESCE(r.method, '') AS method,COALESCE(r.path, '') AS path,
		g.status,g.quota_daily,g.quota_monthly,g.granted_at,g.expires_at,g.version`).
		Joins("LEFT JOIN api_route r ON r.tenant_id=g.tenant_id AND r.id=g.route_id").
		Where("g.tenant_id=? AND g.client_id=?", a.TenantID, client).
		Order("route_code ASC,g.id ASC").Find(&out).Error
	return out, gatewayDBError(err)
}

func (s *gatewayStore) SaveRoute(ctx context.Context, a gateway.Actor, in gateway.Route) (gateway.Route, error) {
	defer s.state.invalidate()
	if in.Currency == "" {
		in.Currency = "CNY"
	}
	if in.UnitsPerCall == 0 {
		in.UnitsPerCall = 1
	}
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
			result := q.Table("api_route").Where("tenant_id=? AND id=? AND version=?", a.TenantID, in.ID, in.Version).Updates(map[string]any{"name": in.Name, "visibility": in.Visibility, "status": in.Status, "billing_mode": in.BillingMode, "price_cents": in.PriceCents, "units_per_call": in.UnitsPerCall, "currency": in.Currency, "version": in.Version + 1, "updated_at": time.Now().UTC()})
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

func (s *gatewayStore) RevokeGrant(ctx context.Context, a gateway.Actor, client, grantID int64) error {
	return s.Transaction(ctx, func(tx context.Context) error {
		current, err := s.LockClient(tx, a.TenantID, client)
		if err != nil {
			return err
		}
		if current.OwnerUserID != a.UserID {
			return gateway.ResourceUnavailable()
		}
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		var before struct {
			ID           int64
			RouteID      int64
			Status       string
			QuotaDaily   int64
			QuotaMonthly int64
			Version      int
			GrantedBy    int64
			GrantedAt    time.Time
			ExpiresAt    *time.Time
		}
		result := q.Table("api_client_route").Clauses(clause.Returning{Columns: []clause.Column{
			{Name: "id"}, {Name: "route_id"}, {Name: "status"}, {Name: "quota_daily"}, {Name: "quota_monthly"},
			{Name: "version"}, {Name: "granted_by"}, {Name: "granted_at"}, {Name: "expires_at"},
		}}).Where("tenant_id=? AND client_id=? AND id=?", a.TenantID, client, grantID).Delete(&before)
		if err := gatewayMutationError(result); err != nil {
			return err
		}
		return s.Audit(tx, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: "gateway.route.revoke", ResourceType: "api_client_route", ResourceID: strconv.FormatInt(client, 10) + ":" + strconv.FormatInt(before.RouteID, 10), Before: map[string]any{"id": before.ID, "client_id": client, "route_id": before.RouteID, "status": before.Status, "quota_daily": before.QuotaDaily, "quota_monthly": before.QuotaMonthly, "version": before.Version, "granted_by": before.GrantedBy, "granted_at": before.GrantedAt, "expires_at": before.ExpiresAt}, Result: "success"})
	})
}

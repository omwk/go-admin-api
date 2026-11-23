package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"gorm.io/gorm"
)

func (s *gatewayStore) ObservationClient(ctx context.Context, scope gateway.ObservationScope, id int64) (gateway.Client, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	q = q.Table("api_client c").Select(gatewayClientColumns).Where("c.id=?", id)
	if !scope.CrossTenant {
		q = q.Where("c.tenant_id=?", scope.TenantID)
	}
	var row gatewayClientRow
	err := q.Take(&row).Error
	row.Client.IPWhitelist = []string(row.Networks)
	return row.Client, gatewayDBError(err)
}

func (s *gatewayStore) ObservationRoute(ctx context.Context, tenant, id int64) (gateway.Route, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var route gateway.Route
	err := q.Table("api_route").Select(gatewayRouteColumns).Where("tenant_id=? AND id=?", tenant, id).Take(&route).Error
	return route, gatewayDBError(err)
}

func observationQuery(q *gorm.DB, scope gateway.ObservationScope, f gateway.CallFilter) *gorm.DB {
	q = q.Table("api_call_log l").Where("l.called_at>=? AND l.called_at<?", f.From, f.To)
	if !scope.CrossTenant {
		q = q.Where("l.tenant_id=?", scope.TenantID)
	}
	if f.ClientID > 0 {
		q = q.Where("l.client_id=?", f.ClientID)
	}
	if f.RouteID > 0 {
		q = q.Where("l.route_id=?", f.RouteID)
	}
	if f.TraceID != "" {
		q = q.Where("l.trace_id=?", f.TraceID)
	}
	if f.Status == "success" {
		q = q.Where("l.status_code>=200 AND l.status_code<300")
	} else if f.Status == "failed" {
		q = q.Where("l.finished_at IS NOT NULL AND (l.status_code<200 OR l.status_code>=300)")
	}
	return q
}

func (s *gatewayStore) ListCalls(ctx context.Context, scope gateway.ObservationScope, f gateway.CallFilter) (gateway.CallPage, error) {
	out := gateway.CallPage{List: []gateway.CallInfo{}, Page: f.Page, PageSize: f.PageSize}
	err := s.Transaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		// Count and page observe one snapshot even while calls are finishing.
		if err := q.Exec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY").Error; err != nil {
			return gatewayDBError(err)
		}
		if err := observationQuery(q, scope, f).Count(&out.Total).Error; err != nil {
			return gatewayDBError(err)
		}
		columns := "l.id,coalesce(l.call_id,'') AS call_id,l.client_id,coalesce(c.name,'') AS client_name,l.route_id,coalesce(r.code,'') AS route_code,l.method,l.path,l.status_code,l.latency_ms,l.billing_mode,l.charge_source,l.cost_units,l.cost_cents,'CNY' AS currency,l.error_code,l.called_at,l.trace_id,l.state,(l.charge_source='manual_test') AS test"
		return gatewayDBError(observationQuery(q, scope, f).Select(columns).
			Joins("LEFT JOIN api_client c ON c.tenant_id=l.tenant_id AND c.id=l.client_id").
			Joins("LEFT JOIN api_route r ON r.tenant_id=l.tenant_id AND r.id=l.route_id").
			Order("l.called_at DESC,l.id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&out.List).Error)
	})
	return out, err
}

func (s *gatewayStore) SummarizeCalls(ctx context.Context, scope gateway.ObservationScope, f gateway.CallFilter) (gateway.CallSummary, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	groups := map[string]string{"client": "l.client_id::text", "route": "l.route_id::text", "day": "to_char(l.called_at AT TIME ZONE 'UTC','YYYY-MM-DD')"}
	group, ok := groups[f.GroupBy]
	if !ok {
		return gateway.CallSummary{}, gateway.Invalid()
	}
	out := gateway.CallSummary{Items: []gateway.CallSummaryItem{}}
	columns := group + " AS key,count(*) AS calls,count(*) FILTER (WHERE l.status_code>=200 AND l.status_code<300)::double precision/count(*) AS success_rate,percentile_cont(0.95) WITHIN GROUP (ORDER BY l.latency_ms) AS p95_latency_ms,coalesce(sum(l.cost_cents),0) AS cost_cents,'CNY' AS currency"
	err := observationQuery(q, scope, f).Where("l.finished_at IS NOT NULL").Select(columns).Group(group).Order("key").Scan(&out.Items).Error
	for _, item := range out.Items {
		out.TotalCalls += item.Calls
	}
	return out, gatewayDBError(err)
}

var _ gateway.ObservationStore = (*gatewayStore)(nil)

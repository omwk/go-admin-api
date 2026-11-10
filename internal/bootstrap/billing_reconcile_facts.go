package bootstrap

import (
	"context"
	"database/sql"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/billing"
)

// Each lateral subquery counts one independent side; no joins can multiply cents or units.
const billingFactsSQL = `WITH ids AS (
 SELECT call_id FROM api_call WHERE tenant_id=@tenant AND called_at>=@day AND called_at<@end
 UNION SELECT call_id FROM api_call_log WHERE tenant_id=@tenant AND called_at>=@day AND called_at<@end AND call_id IS NOT NULL
 UNION SELECT call_id FROM api_call_meter WHERE tenant_id=@tenant AND called_at>=@day AND called_at<@end
 UNION SELECT call_id FROM call_charge WHERE tenant_id=@tenant AND service_date=@day
 UNION SELECT call_id FROM wallet_txn WHERE tenant_id=@tenant AND call_id<>'' AND coalesce(service_date,posting_date)=@day AND occurred_at<=@cutoff
 UNION SELECT call_id FROM unit_txn WHERE tenant_id=@tenant AND call_id<>'' AND service_date=@day AND occurred_at<=@cutoff
), page AS (SELECT call_id FROM ids WHERE call_id>@after ORDER BY call_id LIMIT 200)
SELECT p.call_id,coalesce(c.app_id,h.app_id,w.app_id,u.app_id,cl.app_id,'') app_id,coalesce(c.currency,h.currency,'CNY') currency,
 CASE WHEN c.finished_at>@cutoff THEN 'processing' ELSE coalesce(c.state,'missing') END state,
 coalesce(c.billing_mode,'free') billing_mode,coalesce(c.admitted,false) admitted,
 coalesce(nullif(nullif(c.charge_source,'none'),''),h.source,'none') source,
 CASE WHEN h.finished_at>@cutoff THEN 'reserved' ELSE coalesce(h.state,'missing') END charge_state,
 (c.call_id IS NOT NULL)::int call_count,l.n log_count,m.n meter_count,m.units meter_units,(h.call_id IS NOT NULL)::int charge_count,
 coalesce(c.price_cents,0) price_cents,coalesce(c.charge_units,0) units,coalesce(h.pre_cents,0) pre_cents,
 CASE WHEN h.finished_at<=@cutoff THEN h.actual_cents ELSE 0 END actual_cents,coalesce(h.reserved_units,0) reserved_units,
 w.n cash_count,w.cents cash_cents,w.fn freeze_count,w.frozen freeze_cents,w.rn release_count,w.unfrozen unfrozen_cents,
 u.n consume_count,u.units consumed_units,u.fn unit_reserve_count,u.reserved unit_reserved,u.rn unit_release_count,u.released unit_released,
 l.cents log_cents,l.units log_units,coalesce(c.deadline_at,@cutoff) deadline_at,
 l.invalid+m.invalid+w.invalid+u.invalid+CASE WHEN h.call_id IS NOT NULL AND c.call_id IS NOT NULL AND
 (h.app_id<>c.app_id OR h.currency<>c.currency OR h.route_id<>c.route_id OR h.route_version<>c.route_version OR h.price_cents<>c.price_cents OR h.units<>c.charge_units OR h.created_at<>c.called_at
 OR (c.finished_at<=@cutoff AND (h.source<>c.charge_source OR h.entitlement_id<>c.entitlement_id OR h.actual_cents<>c.cost_cents OR CASE WHEN c.state='succeeded' THEN h.reserved_units ELSE 0 END<>c.cost_units))) THEN 1 ELSE 0 END invalid_refs
FROM page p
LEFT JOIN api_call c ON c.call_id=p.call_id AND c.tenant_id=@tenant
LEFT JOIN call_charge h ON h.call_id=p.call_id AND h.tenant_id=@tenant
LEFT JOIN LATERAL (
 SELECT min(client_id) client_id,count(*) n,coalesce(sum(CASE WHEN finished_at<=@cutoff THEN cost_cents ELSE 0 END),0) cents,
 coalesce(sum(CASE WHEN finished_at<=@cutoff THEN cost_units ELSE 0 END),0) units,
 count(*) FILTER (WHERE c.call_id IS NOT NULL AND (client_id<>c.client_id OR route_id<>c.route_id OR route_version<>c.route_version OR called_at<>c.called_at OR result_hash<>c.result_hash OR state<>c.state OR (finished_at<=@cutoff AND (metered_units<>c.metered_units OR charge_source<>c.charge_source OR entitlement_id<>c.entitlement_id OR cost_cents<>c.cost_cents OR cost_units<>c.cost_units)))) invalid
 FROM api_call_log WHERE tenant_id=@tenant AND call_id=p.call_id
) l ON true
LEFT JOIN LATERAL (
 SELECT min(client_id) client_id,count(*) n,coalesce(sum(units),0) units,
 count(*) FILTER (WHERE c.call_id IS NOT NULL AND (client_id<>c.client_id OR route_id<>c.route_id OR called_at<>c.called_at)) invalid
 FROM api_call_meter WHERE tenant_id=@tenant AND call_id=p.call_id AND (c.call_id IS NULL OR c.finished_at<=@cutoff)
) m ON true
LEFT JOIN LATERAL (
 SELECT min(v.app_id) app_id,count(*) FILTER (WHERE t.type='settle') n,coalesce(sum(t.amount_cents) FILTER (WHERE t.type='settle'),0) cents,
 count(*) FILTER (WHERE t.type='freeze') fn,coalesce(sum(t.amount_cents) FILTER (WHERE t.type='freeze'),0) frozen,
 count(*) FILTER (WHERE t.type='release') rn,coalesce(-sum(t.frozen_delta_cents) FILTER (WHERE t.type IN ('settle','release')),0) unfrozen,
 count(*) FILTER (WHERE t.biz_type<>'api_call' OR (h.call_id IS NOT NULL AND (t.wallet_id<>h.wallet_id OR v.app_id<>h.app_id OR v.currency<>h.currency)) OR (c.call_id IS NOT NULL AND (v.app_id<>c.app_id OR v.currency<>c.currency OR coalesce(t.service_date,(c.called_at AT TIME ZONE 'UTC')::date)<>(c.called_at AT TIME ZONE 'UTC')::date))) invalid
 FROM wallet_txn t LEFT JOIN wallet v ON v.id=t.wallet_id AND v.tenant_id=t.tenant_id
 WHERE t.tenant_id=@tenant AND t.call_id=p.call_id AND t.occurred_at<=@cutoff
) w ON true
LEFT JOIN LATERAL (
 SELECT min(t.app_id) app_id,count(*) FILTER (WHERE t.type='consume') n,coalesce(sum(t.amount) FILTER (WHERE t.type='consume'),0) units,
 count(*) FILTER (WHERE t.type='reserve') fn,coalesce(sum(t.amount) FILTER (WHERE t.type='reserve'),0) reserved,
 count(*) FILTER (WHERE t.type='release') rn,coalesce(sum(t.amount) FILTER (WHERE t.type='release'),0) released,
 count(*) FILTER (WHERE (h.call_id IS NOT NULL AND (t.entitlement_id<>h.entitlement_id OR t.app_id<>h.app_id OR t.currency<>h.currency)) OR (c.call_id IS NOT NULL AND (t.app_id<>c.app_id OR t.currency<>c.currency OR t.service_date<>(c.called_at AT TIME ZONE 'UTC')::date))) invalid
 FROM unit_txn t WHERE t.tenant_id=@tenant AND t.call_id=p.call_id AND t.occurred_at<=@cutoff
) u ON true
LEFT JOIN api_client cl ON cl.tenant_id=@tenant AND cl.id=coalesce(c.client_id,l.client_id,m.client_id)
ORDER BY p.call_id`

type billingReconcileStore struct{ database *db.DB }

func (s *billingReconcileStore) Facts(ctx context.Context, run billing.ReconcileRun, after string) ([]billing.CallFact, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []billing.CallFact{}
	err := q.Raw(billingFactsSQL, sql.Named("tenant", run.TenantID), sql.Named("day", run.ServiceDate), sql.Named("end", run.ServiceDate.AddDate(0, 0, 1)), sql.Named("cutoff", run.CutoffAt), sql.Named("after", after)).Scan(&rows).Error
	return rows, billingDBError(err)
}

func (s *billingReconcileStore) singleFact(ctx context.Context, run billing.ReconcileRun, id string) (billing.CallFact, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	query := strings.Replace(billingFactsSQL, "WHERE call_id>@after ORDER BY call_id LIMIT 200", "WHERE call_id=@call LIMIT 1", 1)
	rows := []billing.CallFact{}
	err := q.Raw(query, sql.Named("tenant", run.TenantID), sql.Named("day", run.ServiceDate), sql.Named("end", run.ServiceDate.AddDate(0, 0, 1)), sql.Named("cutoff", run.CutoffAt), sql.Named("call", id)).Scan(&rows).Error
	if err != nil {
		return billing.CallFact{}, billing.Unavailable()
	}
	if len(rows) != 1 {
		return billing.CallFact{}, billing.Missing()
	}
	return rows[0], nil
}

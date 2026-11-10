package bootstrap

import (
	"context"
	"database/sql"

	"github.com/omwk/go-admin-api/internal/modules/billing"
)

const statementSourcesSQL = `WITH sources AS (
 SELECT 'call:'||h.call_id source_key,CASE WHEN h.source='wallet' THEN 'call_cash' ELSE 'call_units' END kind,
 h.actual_cents amount_cents,CASE WHEN h.source='wallet' THEN 0 ELSE h.reserved_units END units,
 h.price_cents,h.units quantity,h.route_version,h.entitlement_id,h.call_id,
 coalesce((SELECT t.id FROM wallet_txn t WHERE t.tenant_id=h.tenant_id AND t.call_id=h.call_id AND t.type='settle' ORDER BY t.id LIMIT 1),0) original_txn_id,
 h.service_date,h.finished_at occurred_at,NULL::date target_period
 FROM call_charge h WHERE h.tenant_id=@tenant AND h.app_id=@app AND h.currency=@currency AND h.state='settled'
 UNION ALL
 SELECT 'purchase:'||lpad(t.id::text,20,'0'),'subscription',-t.balance_delta_cents,0,e.purchase_cents,e.total_units,e.plan_version,e.id,'',t.id,
 coalesce(t.service_date,(t.occurred_at AT TIME ZONE 'UTC')::date),t.occurred_at,NULL::date
 FROM wallet_txn t JOIN wallet w ON w.id=t.wallet_id AND w.tenant_id=t.tenant_id
 JOIN entitlement e ON e.id::text=t.biz_id AND e.tenant_id=t.tenant_id AND e.source='purchase'
 WHERE t.tenant_id=@tenant AND w.app_id=@app AND w.currency=@currency AND t.type='adjust' AND t.biz_type='subscription' AND t.balance_delta_cents<0
 UNION ALL
 SELECT 'adjustment:'||lpad(a.id::text,20,'0'),a.kind,CASE WHEN a.kind='refund' THEN -a.amount_cents ELSE a.amount_cents END,0,0,1,0,0,t.call_id,t.id,
 coalesce(t.service_date,(t.occurred_at AT TIME ZONE 'UTC')::date),a.applied_at,a.period_start
 FROM billing_adjustment a JOIN wallet_txn t ON t.id=a.original_txn_id AND t.tenant_id=a.tenant_id
 WHERE a.tenant_id=@tenant AND a.app_id=@app AND a.currency=@currency AND a.status='applied'
), attributed AS (
 SELECT x.*,coalesce((SELECT s.id FROM billing_statement s WHERE s.tenant_id=@tenant AND s.app_id=@app AND s.currency=@currency
 AND s.status='issued' AND s.period_start<=x.service_date AND s.period_end>x.service_date LIMIT 1),0) original_statement_id
 FROM sources x
)
SELECT source_key,CASE WHEN target_period IS NULL AND service_date<@start THEN 'late_'||kind ELSE kind END kind,
 amount_cents,units,price_cents,quantity,route_version,entitlement_id,call_id,original_txn_id,original_statement_id,service_date,occurred_at
FROM attributed x WHERE source_key>@after AND occurred_at<=@cutoff
 AND ((target_period IS NOT NULL AND target_period=@start) OR (target_period IS NULL AND service_date<@end AND (service_date>=@start OR original_statement_id>0)))
 AND NOT EXISTS(SELECT 1 FROM billing_statement_line l WHERE l.tenant_id=@tenant AND l.source_key=x.source_key AND l.statement_id<>@statement)
ORDER BY source_key LIMIT 500`

func (s *billingStatementStore) SourceLines(ctx context.Context, st billing.Statement, after string) ([]billing.StatementLine, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []billing.StatementLine{}
	err := q.Raw(statementSourcesSQL, sql.Named("tenant", st.TenantID), sql.Named("app", st.AppID), sql.Named("currency", st.Currency), sql.Named("start", st.PeriodStart), sql.Named("end", st.PeriodEnd), sql.Named("after", after), sql.Named("cutoff", st.CutoffAt), sql.Named("statement", st.ID)).Scan(&rows).Error
	return rows, billingDBError(err)
}

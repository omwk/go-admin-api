package bootstrap

import (
	"context"
	"database/sql"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/billing"
)

const billingDailySQL = `INSERT INTO usage_daily(tenant_id,app_id,currency,service_date,posting_date,calls,metered_calls,metered_cents,charged_cents,entitlement_units,run_id,cutoff_at)
SELECT @tenant,a.app_id,'CNY',@day,@posting,
 (SELECT count(*) FROM api_call_log l WHERE l.tenant_id=a.tenant_id AND l.client_id=a.id AND l.called_at>=@day AND l.called_at<@end),
 (SELECT count(*) FROM api_call_meter m LEFT JOIN api_call c ON c.tenant_id=m.tenant_id AND c.call_id=m.call_id WHERE m.tenant_id=a.tenant_id AND m.client_id=a.id AND m.called_at>=@day AND m.called_at<@end AND (c.finished_at<=@cutoff OR c.call_id IS NULL)),
 (SELECT coalesce(sum(c.price_cents::numeric*c.charge_units),0) FROM api_call c JOIN api_call_meter m ON m.tenant_id=c.tenant_id AND m.call_id=c.call_id WHERE c.tenant_id=a.tenant_id AND c.app_id=a.app_id AND c.called_at>=@day AND c.called_at<@end AND c.finished_at<=@cutoff),
 (SELECT coalesce(sum(t.amount_cents),0) FROM wallet_txn t JOIN wallet w ON w.tenant_id=t.tenant_id AND w.id=t.wallet_id LEFT JOIN api_call c ON c.call_id=t.call_id AND c.tenant_id=t.tenant_id WHERE t.tenant_id=a.tenant_id AND w.app_id=a.app_id AND t.type='settle' AND t.biz_type='api_call' AND coalesce(t.service_date,(c.called_at AT TIME ZONE 'UTC')::date,t.posting_date)=@day AND t.occurred_at<=@cutoff),
 (SELECT coalesce(sum(t.amount),0) FROM unit_txn t WHERE t.tenant_id=a.tenant_id AND t.app_id=a.app_id AND t.type='consume' AND t.service_date=@day AND t.occurred_at<=@cutoff),@run,@cutoff
FROM api_client a WHERE a.tenant_id=@tenant
ON CONFLICT (tenant_id,app_id,currency,service_date) DO UPDATE SET posting_date=EXCLUDED.posting_date,calls=EXCLUDED.calls,metered_calls=EXCLUDED.metered_calls,metered_cents=EXCLUDED.metered_cents,charged_cents=EXCLUDED.charged_cents,entitlement_units=EXCLUDED.entitlement_units,run_id=EXCLUDED.run_id,cutoff_at=EXCLUDED.cutoff_at
WHERE usage_daily.run_id<EXCLUDED.run_id`

func (s *billingReconcileStore) aggregateDay(ctx context.Context, run billing.ReconcileRun) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return billingDBError(q.Exec(billingDailySQL, sql.Named("tenant", run.TenantID), sql.Named("day", run.ServiceDate), sql.Named("end", run.ServiceDate.AddDate(0, 0, 1)), sql.Named("posting", run.PostingDate), sql.Named("cutoff", run.CutoffAt), sql.Named("run", run.ID)).Error)
}

func (s *billingReconcileStore) DailyUsage(ctx context.Context, a billing.Actor, date string) ([]billing.DailyUsage, error) {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, billing.Invalid()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []billing.DailyUsage{}
	err := q.Table("usage_daily").Select("tenant_id,app_id,currency,service_date,posting_date,calls,metered_calls,metered_cents,charged_cents,entitlement_units,run_id,cutoff_at").Where("tenant_id=? AND service_date=? AND app_id IN (SELECT app_id FROM api_client WHERE tenant_id=? AND owner_user_id=?)", a.TenantID, date, a.TenantID, a.UserID).Order("app_id").Limit(100).Find(&rows).Error
	return rows, billingDBError(err)
}

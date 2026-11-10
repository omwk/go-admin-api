package bootstrap

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"

	"github.com/omwk/go-admin-api/internal/modules/billing"
)

const billingControlsSQL = `WITH cash AS (
 SELECT wallet_id,coalesce(sum(balance_delta_cents),0) balance,coalesce(sum(frozen_delta_cents),0) frozen FROM wallet_txn WHERE tenant_id=@tenant GROUP BY wallet_id
), cash_chain AS (
 SELECT wallet_id,version_after,balance_after_cents,frozen_after_cents,
 sum(balance_delta_cents) OVER (PARTITION BY wallet_id ORDER BY version_after,id) b,
 sum(frozen_delta_cents) OVER (PARTITION BY wallet_id ORDER BY version_after,id) f,
 row_number() OVER (PARTITION BY wallet_id ORDER BY version_after,id)+1 v FROM wallet_txn WHERE tenant_id=@tenant
), units AS (
 SELECT entitlement_id,sum(remaining_delta) remaining,sum(reserved_delta) reserved FROM unit_txn WHERE tenant_id=@tenant GROUP BY entitlement_id
), unit_chain AS (
 SELECT entitlement_id,remaining_after,reserved_after,version_after,
 sum(remaining_delta) OVER (PARTITION BY entitlement_id ORDER BY version_after,id) r,
 sum(reserved_delta) OVER (PARTITION BY entitlement_id ORDER BY version_after,id) f,
 row_number() OVER (PARTITION BY entitlement_id ORDER BY version_after,id) v FROM unit_txn WHERE tenant_id=@tenant
), observations AS (
 SELECT w.app_id,w.currency,w.id reference_id,'wallet_balance' kind,coalesce(c.balance,0) expected,w.balance_cents actual FROM wallet w LEFT JOIN cash c ON c.wallet_id=w.id WHERE w.tenant_id=@tenant
 UNION ALL SELECT w.app_id,w.currency,w.id,'wallet_frozen',coalesce(c.frozen,0),w.frozen_cents FROM wallet w LEFT JOIN cash c ON c.wallet_id=w.id WHERE w.tenant_id=@tenant
 UNION ALL SELECT w.app_id,w.currency,w.id,'wallet_chain',0,count(*) FROM cash_chain c JOIN wallet w ON w.id=c.wallet_id AND w.tenant_id=@tenant WHERE c.b<>c.balance_after_cents OR c.f<>c.frozen_after_cents OR c.v<>c.version_after GROUP BY w.app_id,w.currency,w.id
 UNION ALL SELECT e.app_id,e.currency,e.id,'unit_balance',coalesce(u.remaining,0),e.remaining_units FROM entitlement e LEFT JOIN units u ON u.entitlement_id=e.id WHERE e.tenant_id=@tenant
 UNION ALL SELECT e.app_id,e.currency,e.id,'unit_reserved_balance',coalesce(u.reserved,0),e.reserved_units FROM entitlement e LEFT JOIN units u ON u.entitlement_id=e.id WHERE e.tenant_id=@tenant
 UNION ALL SELECT e.app_id,e.currency,e.id,'unit_chain',0,count(*) FROM unit_chain c JOIN entitlement e ON e.id=c.entitlement_id AND e.tenant_id=@tenant WHERE c.r<>c.remaining_after OR c.f<>c.reserved_after OR c.v<>c.version_after GROUP BY e.app_id,e.currency,e.id
 UNION ALL SELECT e.app_id,e.currency,e.id,'purchase_count',1,count(t.id) FROM entitlement e LEFT JOIN wallet_txn t ON t.tenant_id=e.tenant_id AND t.biz_type='subscription' AND t.biz_id=e.id::text WHERE e.tenant_id=@tenant AND e.source='purchase' GROUP BY e.app_id,e.currency,e.id
 UNION ALL SELECT e.app_id,e.currency,e.id,'purchase_cents',e.purchase_cents,coalesce(-sum(t.balance_delta_cents),0) FROM entitlement e LEFT JOIN wallet_txn t ON t.tenant_id=e.tenant_id AND t.biz_type='subscription' AND t.biz_id=e.id::text WHERE e.tenant_id=@tenant AND e.source='purchase' GROUP BY e.app_id,e.currency,e.id
 UNION ALL SELECT w.app_id,w.currency,t.id,'orphan_purchase',1,0 FROM wallet_txn t JOIN wallet w ON w.id=t.wallet_id AND w.tenant_id=t.tenant_id LEFT JOIN entitlement e ON e.tenant_id=t.tenant_id AND e.id::text=t.biz_id AND e.source='purchase' WHERE t.tenant_id=@tenant AND t.biz_type='subscription' AND e.id IS NULL
)
SELECT app_id,currency,reference_id,kind,expected,actual FROM observations WHERE expected<>actual`

func (s *billingReconcileStore) Controls(ctx context.Context, run billing.ReconcileRun) ([]billing.ControlFact, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []billing.ControlFact{}
	err := q.Raw(billingControlsSQL, sql.Named("tenant", run.TenantID)).Scan(&rows).Error
	return rows, billingDBError(err)
}

func billingObservationHash(d billing.Difference) string {
	raw, err := json.Marshal([]any{d.AppID, d.Currency, d.CallID, d.Kind, d.Expected, d.Actual, d.ReferenceID})
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

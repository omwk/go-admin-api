package bootstrap

import (
	"context"
	"net/http"
	"strconv"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"gorm.io/gorm/clause"
)

func (s *gatewayStore) LockSecretForReveal(ctx context.Context, tenantID, clientID, secretID int64) (gateway.Secret, error) {
	if !db.InTransaction(ctx) {
		return gateway.Secret{}, gateway.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	// Serialize each tenant's quota check and audit across API instances.
	if err := q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "gateway:secret:reveal:"+strconv.FormatInt(tenantID, 10)).Error; err != nil {
		return gateway.Secret{}, gatewayDBError(err)
	}
	var key gateway.Secret
	err := q.Table("api_client_secret").Select(gatewayKeyColumns).
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where("tenant_id=? AND client_id=? AND id=?", tenantID, clientID, secretID).
		Take(&key).Error
	return key, gatewayDBError(err)
}

func (s *gatewayStore) RecordSecretReveal(ctx context.Context, event audit.Event, dailyLimit int) error {
	if !db.InTransaction(ctx) {
		return gateway.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	// Audit created_at uses the same PostgreSQL transaction timestamp as now().
	const dayStart = "(date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')"
	var count int64
	err := q.Table("audit_log").Where("tenant_id=? AND action=? AND result='success'", event.TenantID, event.Action).
		Where("created_at >= " + dayStart + " AND created_at < " + dayStart + " + interval '24 hours'").Count(&count).Error
	if err != nil {
		return gatewayDBError(err)
	}
	if count >= int64(dailyLimit) {
		return errs.New(errs.ErrTooManyRequests, http.StatusTooManyRequests, "已达到今日密钥查看次数上限")
	}
	// Retain the IDs and source IP without copying request headers or display names.
	ctx = audit.WithRequestMeta(ctx, audit.RequestMeta{})
	if err := audit.Record(ctx, s.database, event); err != nil {
		return gateway.Unavailable()
	}
	return nil
}

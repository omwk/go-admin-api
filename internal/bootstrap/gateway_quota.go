package bootstrap

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type gatewayQuotaRow struct {
	TenantID, ClientID int64
	QuotaKey           string
	AdmittedCount      int64
	ResetAt            time.Time
}

func (s *gatewayStore) ConsumeQuota(ctx context.Context, call gateway.Call) error {
	if !db.InTransaction(ctx) {
		return gateway.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var limits gateway.QuotaLimits
	err := q.Table("api_client_route").Select("quota_daily AS daily,quota_monthly AS monthly").
		Clauses(clause.Locking{Strength: "SHARE"}).Where("tenant_id=? AND client_id=? AND route_id=? AND status='active' AND (expires_at IS NULL OR expires_at>?)",
		call.TenantID, call.ClientID, call.RouteID, call.CalledAt).Take(&limits).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errs.New(80104, 403, "未获授权访问此接口")
	}
	if err != nil {
		return gatewayDBError(err)
	}
	rows, err := lockGatewayQuotas(q, call)
	if err != nil {
		return err
	}
	var denied error
	for i, limit := range []int64{limits.Daily, limits.Monthly} {
		if limit > 0 && rows[i].AdmittedCount >= limit {
			kind := []string{"daily_quota", "monthly_quota"}[i]
			denied = gateway.QuotaExceeded(kind, call.CalledAt, rows[i].ResetAt, limit)
		}
	}
	if denied != nil {
		return denied
	}
	return gatewayDBError(q.Table("api_quota").Where("tenant_id=? AND client_id=? AND quota_key IN ?", call.TenantID, call.ClientID,
		[]string{rows[0].QuotaKey, rows[1].QuotaKey}).Update("admitted_count", gorm.Expr("admitted_count+1")).Error)
}

func lockGatewayQuotas(q *gorm.DB, call gateway.Call) ([]gatewayQuotaRow, error) {
	utc := call.CalledAt.UTC()
	day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	month := time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
	prefix := "quota:" + strconv.FormatInt(call.ClientID, 10) + ":"
	rows := []gatewayQuotaRow{
		{TenantID: call.TenantID, ClientID: call.ClientID, QuotaKey: prefix + day.Format("2006-01-02"), ResetAt: day.AddDate(0, 0, 1)},
		{TenantID: call.TenantID, ClientID: call.ClientID, QuotaKey: prefix + month.Format("2006-01"), ResetAt: month.AddDate(0, 1, 0)},
	}
	for i := range rows {
		if err := q.Table("api_quota").Clauses(clause.OnConflict{DoNothing: true}).Create(&rows[i]).Error; err != nil {
			return nil, gatewayDBError(err)
		}
		if err := q.Table("api_quota").Select("tenant_id,client_id,quota_key,admitted_count,reset_at").Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id=? AND client_id=? AND quota_key=?", call.TenantID, call.ClientID, rows[i].QuotaKey).Take(&rows[i]).Error; err != nil {
			return nil, gatewayDBError(err)
		}
	}
	return rows, nil
}

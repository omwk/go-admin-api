package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/billing"
)

func (s *billingStore) InsertCode(ctx context.Context, c *billing.Code) error {
	if !db.InTransaction(ctx) {
		return billing.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return billingDBError(q.Table("recharge_code").Create(c).Error)
}

func (s *billingStore) UseCode(ctx context.Context, tenant int64, app, hash string, at time.Time) (billing.Code, error) {
	if !db.InTransaction(ctx) {
		return billing.Code{}, billing.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var out billing.Code
	result := q.Raw(`UPDATE recharge_code SET status='used',used_by_app_id=?,used_at=?
		WHERE tenant_id=? AND code_hash=? AND status='unused' AND (expires_at IS NULL OR expires_at>?)
		RETURNING id,tenant_id,code_hash,amount_cents,currency,status,used_by_app_id,used_at,expires_at,created_by,created_at`, app, at, tenant, hash, at).Scan(&out)
	if result.Error != nil {
		return out, billing.Unavailable()
	}
	if result.RowsAffected != 1 {
		return out, billing.InvalidCode()
	}
	return out, nil
}

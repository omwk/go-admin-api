package bootstrap

import (
	"context"
	"strings"

	"github.com/omwk/go-admin-api/internal/modules/billing"
)

func (s *billingStore) Entitlements(ctx context.Context, a billing.Actor, app string) ([]billing.Entitlement, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []billing.Entitlement{}
	query := q.Table("entitlement e").Joins("JOIN api_client c ON c.tenant_id=e.tenant_id AND c.app_id=e.app_id").Where("e.tenant_id=? AND c.owner_user_id=?", a.TenantID, a.UserID)
	if app != "" {
		query = query.Where("e.app_id=?", app)
	}
	err := query.Select("e." + strings.ReplaceAll(entitlementColumns, ",", ",e.")).Order("e.id DESC").Limit(100).Find(&rows).Error
	return rows, billingDBError(err)
}

func (s *billingStore) UnitTxns(ctx context.Context, a billing.Actor, id int64) ([]billing.UnitTxn, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var owned int64
	if err := q.Table("entitlement e").Joins("JOIN api_client c ON c.tenant_id=e.tenant_id AND c.app_id=e.app_id").Where("e.tenant_id=? AND e.id=? AND c.owner_user_id=?", a.TenantID, id, a.UserID).Count(&owned).Error; err != nil {
		return nil, billing.Unavailable()
	}
	if owned != 1 {
		return nil, billing.Missing()
	}
	rows := []billing.UnitTxn{}
	err := q.Table("unit_txn").Select(unitTxnColumns).Where("tenant_id=? AND entitlement_id=?", a.TenantID, id).Order("id DESC").Limit(100).Find(&rows).Error
	return rows, billingDBError(err)
}

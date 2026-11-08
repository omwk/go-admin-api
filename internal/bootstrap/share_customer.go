package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/share"
	"gorm.io/gorm"
)

const customerColumns = "c.id,c.tenant_id,c.code,c.name,c.version,c.status,c.owner_user_id,c.source,c.created_at,c.updated_at"

func (s *shareStore) Customers(ctx context.Context, tenant int64, in share.Query) ([]share.Customer, int64, error) {
	base, cancel, err := s.base(ctx, tenant, "customer", "Trade.Customers")
	if err != nil {
		return nil, 0, err
	}
	defer cancel()
	base = shareFilter(base, "c", in)
	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []share.Customer{}
	err = base.Select(customerColumns).Order(shareOrder("c", in.Sort)).Offset((in.Page - 1) * in.PageSize).Limit(in.PageSize).Find(&rows).Error
	return rows, total, shareDBError(err)
}

func (s *shareStore) Customer(ctx context.Context, tenant, id int64, lock bool) (share.Customer, error) {
	q, cancel, err := s.base(ctx, tenant, "customer", "Trade.Customer")
	if err != nil {
		return share.Customer{}, err
	}
	defer cancel()
	var row share.Customer
	err = shareLock(q, "c", lock).Select(customerColumns).Where("c.id=?", id).Take(&row).Error
	return row, shareDBError(err)
}

func (s *shareStore) SaveCustomer(ctx context.Context, row share.Customer, create bool) error {
	if err := s.ValidateOwner(ctx, row.TenantID, row.OwnerUserID); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if create {
		if row.Status != "active" {
			now := time.Now().UTC()
			row.TerminatedAt = &now
		}
		return shareDBError(q.Create(&row).Error)
	}
	previous, err := s.Customer(ctx, row.TenantID, row.ID, true)
	if err != nil {
		return err
	}
	if row.Status != "active" || previous.OwnerUserID != row.OwnerUserID {
		if err := s.revokeParentLinks(ctx, row.TenantID, row.ID, "customer", "customer_changed"); err != nil {
			return err
		}
	}
	terminated := any(nil)
	if row.Status != "active" {
		terminated = gorm.Expr("COALESCE(terminated_at,?)", time.Now().UTC())
	}
	return shareChanged(q.Model(&share.Customer{}).Where("tenant_id=? AND id=? AND deleted_at IS NULL AND version=?", row.TenantID, row.ID, row.Version-1).
		Updates(map[string]any{"code": row.Code, "name": row.Name, "version": row.Version, "status": row.Status, "owner_user_id": row.OwnerUserID, "source": row.Source,
			"terminated_at": terminated, "pii_purged_at": nil,
			"contact_name_cipher": row.ContactName.Cipher, "contact_name_nonce": row.ContactName.Nonce, "phone_cipher": row.Phone.Cipher, "phone_nonce": row.Phone.Nonce,
			"email_cipher": row.Email.Cipher, "email_nonce": row.Email.Nonce, "key_version": row.KeyVersion, "updated_at": time.Now().UTC()}))
}

func (s *shareStore) DeleteCustomer(ctx context.Context, tenant, id int64, version int) error {
	row, err := s.Customer(ctx, tenant, id, true)
	if err != nil {
		return err
	}
	if version <= 0 || row.Version != version {
		return share.VersionConflict()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if err := s.revokeParentLinks(ctx, tenant, id, "customer", "customer_deleted"); err != nil {
		return err
	}
	return shareChanged(q.Model(&share.Customer{}).Where("tenant_id=? AND id=? AND deleted_at IS NULL AND version=?", tenant, id, version).
		Updates(map[string]any{"deleted_at": time.Now().UTC(), "updated_at": time.Now().UTC(), "status": "disabled", "terminated_at": gorm.Expr("COALESCE(terminated_at,?)", time.Now().UTC())}))
}

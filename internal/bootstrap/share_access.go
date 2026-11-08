package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/share"
)

func (s *shareStore) Admit(ctx context.Context, observed share.Link, root bool, now time.Time) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var current share.Link
	err := shareLock(q.Table("share_link l"), "l", true).Select(shareLinkColumns).
		Where("l.tenant_id=? AND l.id=?", observed.TenantID, observed.ID).Take(&current).Error
	if err != nil {
		return shareDBError(err)
	}
	if current.Status != "active" || current.ExpiresAt != nil && !now.Before(*current.ExpiresAt) ||
		current.TokenHash != observed.TokenHash || current.ContentVersion != observed.ContentVersion || current.AllowInquiry != observed.AllowInquiry || !samePassword(current.PasswordHash, observed.PasswordHash) {
		return share.AccessDenied()
	}
	frozen, err := s.Frozen(ctx, current.TenantID, current.ID)
	if err != nil {
		return err
	}
	if frozen.Content.ContentVersion != observed.FrozenVersion {
		return share.AccessDenied()
	}
	if err := s.activeShareParents(ctx, current, frozen, now); err != nil {
		return err
	}
	if !root {
		return nil
	}
	result := q.Table("share_link").Where("tenant_id=? AND id=? AND status='active' AND (expires_at IS NULL OR expires_at>?) AND (max_views=0 OR view_count<max_views)", current.TenantID, current.ID, now).
		UpdateColumn("view_count", current.ViewCount+1)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return share.AccessDenied()
	}
	return nil
}

func samePassword(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (s *shareStore) activeShareParents(ctx context.Context, link share.Link, frozen share.Frozen, now time.Time) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if link.CustomerID != nil {
		var count int64
		if err := q.Table("customer").Where("tenant_id=? AND id=? AND status='active' AND deleted_at IS NULL", link.TenantID, *link.CustomerID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return share.AccessDenied()
		}
	}
	ids := map[int64]bool{}
	if link.PriceListID != nil {
		ids[*link.PriceListID] = true
	}
	for _, evidence := range frozen.Evidence {
		if evidence.PriceListID > 0 {
			ids[evidence.PriceListID] = true
		}
	}
	for id := range ids {
		base := q.Table("price_list").Where("tenant_id=? AND id=? AND status='active' AND deleted_at IS NULL AND currency=? AND (valid_from IS NULL OR valid_from<=?) AND (valid_to IS NULL OR valid_to>?)",
			link.TenantID, id, frozen.Content.Currency, now, now)
		if link.CustomerID == nil {
			base = base.Where("type='default' AND customer_id IS NULL")
		} else {
			base = base.Where("(type='default' AND customer_id IS NULL) OR (type='customer' AND customer_id=?)", *link.CustomerID)
		}
		var count int64
		if err := base.Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return share.AccessDenied()
		}
	}
	return nil
}

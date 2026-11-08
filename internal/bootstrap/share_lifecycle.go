package bootstrap

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/share"
)

func (s *shareStore) revokeParentLinks(ctx context.Context, tenant, parent int64, kind, reason string) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var after int64
	for {
		rows := []struct {
			ID      int64
			Payload string
		}{}
		query := q.Table("share_link l").Select("l.id,s.payload").Joins("JOIN share_snapshot s ON s.tenant_id=l.tenant_id AND s.share_link_id=l.id").
			Where("l.tenant_id=? AND l.status='active' AND l.id>?", tenant, after)
		if kind == "customer" {
			query = query.Where("l.customer_id=?", parent)
		}
		if err := query.Order("l.id").Limit(100).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			after = row.ID
			if kind == "price_list" {
				var frozen share.Frozen
				if err := json.Unmarshal([]byte(row.Payload), &frozen); err != nil {
					return err
				}
				if !shareReferencesList(frozen, parent) {
					continue
				}
			}
			now := time.Now().UTC()
			r := q.Table("share_link").Where("tenant_id=? AND id=? AND status='active'", tenant, row.ID).Updates(map[string]any{"status": "revoked", "revoked_at": now})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected == 0 {
				continue
			}
			user := shareActorUser(ctx)
			e := audit.Event{TenantID: tenant, ActorType: "user", ActorID: &user, Action: "share.link.lifecycle.revoke", ResourceType: "share_link", ResourceID: strconv.FormatInt(row.ID, 10),
				After: map[string]any{"reason": reason, "parent_type": kind, "parent_id": parent, "revoked_at": now}, Result: "success"}
			if err := s.Audit(ctx, e); err != nil {
				return err
			}
		}
	}
}

func shareReferencesList(frozen share.Frozen, id int64) bool {
	if frozen.Policy.PriceListID == id {
		return true
	}
	for _, source := range frozen.Evidence {
		if source.PriceListID == id {
			return true
		}
	}
	return false
}

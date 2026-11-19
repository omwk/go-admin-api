package bootstrap

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// tenantPurgeTables is the single ordered deletion manifest. tenant is always last.
// Raw table deletes also remove soft-deleted users; every statement binds the target ID.
func tenantPurgeTables() []string {
	return []string{
		"sys_role_permission", "sys_role_menu", "sys_user_role", "sys_role_dept",
		"password_history", "sys_refresh_token", "auth_token",
		"notify_delivery", "notify_outbox", "notify_template", "channel_provider",
		"notification", "notification_preference", "sys_user_channel", "sys_dict_item", "sys_dict",
		"sys_setting", "outbox_event", "idempotency_key",
		"sys_menu", "sys_role", "sys_user", "sys_dept", "sys_permission", "tenant",
	}
}

func (s *tenantStore) LockTenantLifecycle(ctx context.Context, id int64) error {
	// Invite acceptance takes this lock before its tenant SHARE lock.
	if err := s.LockInviteTenant(ctx, id); err != nil {
		return err
	}
	q, cancel := s.query(ctx)
	defer cancel()
	return q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", fmt.Sprintf("identity:management:%d", id)).Error
}

func (s *tenantStore) ArchiveTenant(ctx context.Context, id int64, at *time.Time) error {
	q, cancel := s.query(ctx)
	defer cancel()
	status := "suspended"
	if at == nil {
		status = "active"
	}
	return adminMutation(q.Model(&model.Tenant{}).Where("id=?", id).
		Updates(map[string]any{"deleted_at": at, "status": status}))
}

func (s *tenantStore) PurgeTenant(ctx context.Context, id int64) (map[string]int64, error) {
	if err := s.checkTenantPurgeBusiness(ctx, id); err != nil {
		return nil, err
	}
	q, cancel := s.query(ctx)
	defer cancel()
	counts := map[string]int64{}
	for _, table := range tenantPurgeTables() {
		column := "tenant_id"
		if table == "tenant" {
			column = "id"
		}
		// Identifiers come exclusively from the manifest above, never from requests.
		filter := column + "=?"
		var count int64
		if err := q.Table(table).Where(filter, id).Count(&count).Error; err != nil {
			return nil, fmt.Errorf("count tenant table %s: %w", table, err)
		}
		result := q.Exec("DELETE FROM "+table+" WHERE "+filter, id)
		if result.Error != nil {
			return nil, fmt.Errorf("purge tenant table %s: %w", table, result.Error)
		}
		if result.RowsAffected != count {
			return nil, errs.New(errs.ErrConflict, 409, "租户数据发生变化，请重试")
		}
		counts[table] = count
	}
	return counts, nil
}

func (s *tenantStore) checkTenantPurgeBusiness(ctx context.Context, id int64) error {
	q, cancel := s.query(ctx)
	defer cancel()
	blocked := []string{}
	for table, exemption := range tenantPurgeExemptions() {
		if !exemption.retain {
			blocked = append(blocked, table)
		}
	}
	sort.Strings(blocked)
	for _, table := range blocked {
		var present bool
		if err := q.Raw("SELECT EXISTS (SELECT 1 FROM "+table+" WHERE tenant_id=?)", id).Scan(&present).Error; err != nil {
			return err
		}
		if present {
			return errs.New(errs.ErrConflict, 409, "租户仍有业务数据，暂不支持彻底删除")
		}
	}
	return nil
}

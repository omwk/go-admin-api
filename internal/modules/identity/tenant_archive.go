package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// ArchiveTenant suspends access without removing tenant data or resetting the grace period.
// The caller supplies the management transaction, shared with audit and idempotency.
func (s *TenantService) ArchiveTenant(ctx context.Context, actor Overview, id int64) (model.Tenant, error) {
	if err := s.global(actor); err != nil {
		return model.Tenant{}, err
	}
	if id == s.platformID {
		return model.Tenant{}, errs.New(errs.ErrConflict, 409, "平台租户不可归档")
	}
	if err := s.store.LockTenantLifecycle(ctx, id); err != nil {
		return model.Tenant{}, err
	}
	before, err := s.store.Tenant(ctx, id, true)
	if err != nil || before.DeletedAt != nil {
		return before, err
	}
	now := s.now().UTC()
	return s.setTenantArchived(ctx, actor, before, &now, "system.tenant.archive")
}

// RestoreTenant reopens an archived tenant only for the configured platform administrator.
func (s *TenantService) RestoreTenant(ctx context.Context, actor Overview, id int64) (model.Tenant, error) {
	if err := s.global(actor); err != nil {
		return model.Tenant{}, err
	}
	if err := s.store.LockTenantLifecycle(ctx, id); err != nil {
		return model.Tenant{}, err
	}
	before, err := s.store.Tenant(ctx, id, true)
	if err != nil {
		return before, err
	}
	if before.DeletedAt == nil {
		return model.Tenant{}, errs.New(errs.ErrConflict, 409, "租户未归档")
	}
	return s.setTenantArchived(ctx, actor, before, nil, "system.tenant.restore")
}

func (s *TenantService) setTenantArchived(ctx context.Context, actor Overview, before model.Tenant, at *time.Time, action string) (model.Tenant, error) {
	if err := s.store.ArchiveTenant(ctx, before.ID, at); err != nil {
		return model.Tenant{}, err
	}
	row, err := s.store.Tenant(ctx, before.ID, false)
	if err != nil {
		return row, err
	}
	previous := map[string]any{"status": before.Status, "deleted_at": before.DeletedAt}
	next := map[string]any{"status": row.Status, "deleted_at": row.DeletedAt}
	return row, s.record(ctx, actor, action, "tenant", row.ID, row.ID, previous, next, false)
}

// PurgeTenant deletes an archived, eligible tenant and records table counts atomically.
// It runs inside Execute's transaction; any storage or audit error rolls everything back.
func (s *TenantService) PurgeTenant(ctx context.Context, actor Overview, id int64, confirm string) error {
	if err := s.global(actor); err != nil {
		return err
	}
	if id == s.platformID {
		return errs.New(errs.ErrConflict, 409, "平台租户不可删除")
	}
	if err := s.store.LockTenantLifecycle(ctx, id); err != nil {
		return err
	}
	row, err := s.store.Tenant(ctx, id, true)
	if err != nil {
		return err
	}
	if row.DeletedAt == nil {
		return errs.New(errs.ErrConflict, 409, "请先归档")
	}
	days, err := setting.TenantPurgeGraceDays(ctx, s.admin.settings, s.platformID)
	if err != nil {
		return err
	}
	if s.now().UTC().Before(row.DeletedAt.Add(time.Duration(days) * 24 * time.Hour)) {
		return errs.New(errs.ErrConflict, 409, fmt.Sprintf("归档未满 %d 天", days))
	}
	if confirm != row.Code {
		return errs.New(errs.ErrInvalidParam, 400, "确认码不匹配")
	}
	rows, err := s.store.PurgeTenant(ctx, id)
	if err != nil {
		return err
	}
	return s.record(ctx, actor, "system.tenant.purge", "tenant", id, id, nil, map[string]any{"code": row.Code, "rows": rows}, false)
}

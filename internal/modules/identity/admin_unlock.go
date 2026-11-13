package identity

import (
	"context"

	"github.com/gin-gonic/gin"
)

// UnlockUser clears login protection in the current management transaction.
// An already clear account succeeds without a second audit record.
func (s *AdminService) UnlockUser(ctx context.Context, actor Overview, id int64) (AdminUser, error) {
	row, err := s.store.User(ctx, actor.User.TenantID, id)
	if err != nil {
		return AdminUser{}, err
	}
	if row.IsSuper && !actor.User.IsSuper {
		return AdminUser{}, denied()
	}
	if row.Status == "disabled" {
		return AdminUser{}, conflictAdmin()
	}
	if row.Status == "active" && row.FailedAttempts == 0 && row.LockedUntil == nil {
		return adminUser(row), nil
	}
	before := map[string]any{"status": row.Status, "failed_attempts": row.FailedAttempts}
	if err := s.store.UpdateUser(ctx, actor.User.TenantID, id, map[string]any{
		"status": "active", "failed_attempts": 0, "locked_until": nil,
	}); err != nil {
		return AdminUser{}, err
	}
	updated, err := s.store.User(ctx, actor.User.TenantID, id)
	if err != nil {
		return AdminUser{}, err
	}
	after := map[string]any{"status": updated.Status, "failed_attempts": updated.FailedAttempts}
	err = s.record(ctx, actor, "system:user:unlock", "user", id, before, after, false)
	return adminUser(updated), err
}

func (h adminHandler) unlockUser(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, actor Overview, id int64, _ noAdminBody) (any, int, error) {
		value, err := h.service.UnlockUser(ctx, actor, id)
		return value, 200, err
	})
}

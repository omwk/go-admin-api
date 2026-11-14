package identity

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

func (s *AuthService) accountLoginDecision(ctx context.Context, user *model.User, found, matches bool, meta RequestMeta, policy setting.Security) (error, error) {
	if found && matches && user.Status == "disabled" {
		if store, ok := s.store.(repo.RegistrationStatus); ok {
			pending, err := store.HasPendingRegistration(ctx, user.TenantID, user.ID)
			if err != nil {
				return nil, err
			}
			if pending {
				return errs.New(errs.ErrTenantSuspended, 403, "请先完成邮箱验证"), nil
			}
		}
	}
	return s.loginDecision(user, found, matches, meta, s.now().UTC(), policy), nil
}

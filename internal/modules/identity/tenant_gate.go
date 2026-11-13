package identity

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

func (s *AuthService) checkTenant(ctx context.Context, tenant int64) error {
	if gate, ok := s.store.(repo.TenantGate); ok {
		return gate.CheckTenant(ctx, tenant)
	}
	return nil
}

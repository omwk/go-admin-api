package repo

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// LoginTenant resolves an exact code, or a legacy ID when code is empty, without filtering status.
type LoginTenant interface {
	TenantForLogin(context.Context, string, int64) (model.Tenant, bool, error)
}

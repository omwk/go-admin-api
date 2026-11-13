package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
)

// InvalidateSeedAuthz advances every default-tenant user's generation after seed commits.
// Failed increments remain warnings for the CLI; other users are still processed.
func InvalidateSeedAuthz(ctx context.Context, database *db.DB, versions *authz.Cache) error {
	if db.InTransaction(ctx) {
		return errors.New("seed authorization invalidation requires committed transactions")
	}
	var pending error
	for last := int64(0); ; {
		q, cancel := database.WithContext(ctx)
		var users []int64
		// Table deliberately includes inactive and soft-deleted users of this tenant.
		err := q.Table("sys_user").Where("tenant_id = ? AND id > ?", identitySeedTenantID, last).
			Order("id").Limit(500).Pluck("id", &users).Error
		cancel()
		if err != nil {
			return errors.Join(pending, fmt.Errorf("load seed authorization users: %w", err))
		}
		if len(users) == 0 {
			return pending
		}
		if versions.Pending(ctx, identitySeedTenantID, users) {
			pending = errors.New("seed authorization invalidation pending")
		}
		if len(users) < 500 {
			return pending
		}
		last = users[len(users)-1]
	}
}

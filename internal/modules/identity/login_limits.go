package identity

import (
	"context"
	"time"
)

// LoginLimits isolates failed login counters by tenant code and normalized account.
// Implementations bound all counters by TTL and atomically count tenant failures.
type LoginLimits interface {
	AllowTenantLogin(context.Context, string) (bool, error)
	RecordLoginFailure(context.Context, string, string, time.Duration) error
	ClearLoginFailures(context.Context, string, string) error
}

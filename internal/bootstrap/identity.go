package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
)

// RegisterIdentity assembles authentication, versioned profiles and scoped management routes.
// The returned closer owns the dedicated atomic-revocation Redis connection pool.
func RegisterIdentity(ctx context.Context, router *gin.Engine, database *db.DB, shared *cache.Cache, cfg *config.Config) (func() error, error) {
	if _, err := scopeRegistry(); err != nil {
		return nil, err
	}
	redis, err := newAuthRedis(ctx, shared, cfg.Redis)
	if err != nil {
		return nil, err
	}
	service, err := identity.NewAuthService(identity.AuthDependencies{
		Store: newAuthStore(database), Redis: redis, Secret: []byte(cfg.JWT.Secret), Now: time.Now,
		Audit: func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, database, event) },
	})
	if err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	// M1 retains the existing default tenant; tenant switching belongs to a later milestone.
	identity.RegisterAuthRoutes(router, service, 1)
	profiles, err := identity.NewProfileService(newProfileStore(database))
	if err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	versions := authz.New(shared)
	profiles.UseAuthz(versions, newProfileStore(database).account)
	identity.RegisterProfileRoutes(router, service, profiles)
	admin, err := identity.NewAdminService(newAdminStore(database), func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, database, event) })
	if err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	admin.UseInvalidator(versions.Pending)
	admin.UseSessions(&sessionStore{admin: newAdminStore(database)}, service)
	if err := identity.RegisterAdminRoutes(router, service, profiles, admin); err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	return redis.markers.Close, nil
}

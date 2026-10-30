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
)

// RegisterIdentity assembles the five M1a authentication and profile routes.
// The returned closer owns the dedicated atomic-revocation Redis connection pool.
func RegisterIdentity(ctx context.Context, router *gin.Engine, database *db.DB, shared *cache.Cache, cfg *config.Config) (func() error, error) {
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
	identity.RegisterProfileRoutes(router, service, profiles)
	return redis.markers.Close, nil
}

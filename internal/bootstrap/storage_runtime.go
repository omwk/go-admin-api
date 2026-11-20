package bootstrap

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/system"
)

func newStorageResolver(database *db.DB, cfg *config.Config) (*storage.Resolver, error) {
	sealer, err := credentialSealer(cfg)
	if err != nil {
		return nil, err
	}
	return storage.NewResolver(&storageStore{database: database}, sealer)
}

func registerStorageRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, cfg *config.Config) error {
	sealer, err := credentialSealer(cfg)
	if err != nil {
		return err
	}
	service, err := system.NewStorageService(&storageStore{database: database}, sealer, func(ctx context.Context, e audit.Event) error { return RecordAudit(ctx, database, e) }, newIdempotencyStore(database))
	if err != nil {
		return err
	}
	return system.RegisterStorageRoutes(router, auth, profiles, service)
}

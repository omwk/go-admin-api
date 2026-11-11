package bootstrap

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	kernelcrypto "github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
	"github.com/omwk/go-admin-api/internal/modules/shop"
)

// RegisterIdentity assembles authentication, versioned profiles and scoped management routes.
// The returned closer owns the dedicated atomic-revocation Redis connection pool.
func RegisterIdentity(ctx context.Context, router *gin.Engine, database *db.DB, shared *cache.Cache, cfg *config.Config, systems ...*httpx.SystemAuthentication) (func() error, error) {
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
	registerIdentityAuthentication(router, service, systems)
	profiles, err := identity.NewProfileService(newProfileStore(database))
	if err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	versions := authz.New(shared)
	profiles.UseAuthz(versions, newProfileStore(database).account)
	identity.RegisterProfileRoutes(router, service, profiles)
	adminStore := newAdminStore(database)
	admin, err := identity.NewAdminService(adminStore, func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, database, event) })
	if err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	admin.UseInvalidator(versions.Pending)
	admin.UseSessions(&sessionStore{admin: newAdminStore(database)}, service)
	if err := registerAccountAndAdmin(router, service, profiles, admin, database, redis, cfg); err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	sealer, err := credentialSealer(cfg)
	if err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	shopService, err := shop.NewService(newShopStore(database), adminStore.Authorize, sealer)
	if err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	shopService.UseIdempotency(newIdempotencyStore(database), func(ctx context.Context, event audit.Event) error {
		return audit.Record(ctx, database, event)
	})
	if err := shop.RegisterRoutes(router, service, profiles, shopService); err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	if err := registerCredentialMaintenanceRoutes(router, service, profiles, database, cfg); err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	if err := registerIdentityFileRoutes(router, service, profiles, database, cfg); err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	if err := registerSyncRoutes(router, service, profiles, database, shared, cfg); err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	if err := catalog.RegisterCategoryRoutes(router, service, profiles, NewCategoryManagementStore(database)); err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	if err := catalog.RegisterInventoryRoutes(router, service, profiles, NewInventoryStore(database)); err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	cursors, err := productCursorSigner(cfg)
	if err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	productService, err := catalog.NewProductService(NewProductStore(database), adminStore.Authorize, cursors)
	if err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	if err := catalog.RegisterProductRoutes(router, service, profiles, productService); err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	if err := registerTradeAndGatewayRoutes(router, service, profiles, database, shared, cfg); err != nil {
		return nil, errors.Join(err, redis.markers.Close())
	}
	return redis.markers.Close, nil
}

func registerIdentityFileRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, cfg *config.Config) error {
	files, err := shop.NewFileService(newFileStore(database), shop.NewHTTPObjectStore(cfg.Storage.Endpoint, cfg.Storage.Bucket, cfg.Storage.AccessKey, cfg.Storage.SecretKey), newIdempotencyStore(database), cfg.Storage.Bucket)
	if err != nil {
		return err
	}
	return shop.RegisterFileRoutes(router, auth, profiles, files)
}

func registerCredentialMaintenanceRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, cfg *config.Config) error {
	runtime, err := newCredentialRefreshRuntime(database, cfg)
	if err != nil {
		return err
	}
	if err := shop.RegisterCredentialRefreshRoutes(router, auth, profiles, runtime); err != nil {
		return err
	}
	return registerCredentialNotificationRoutes(router, auth, profiles, runtime)
}

func registerIdentityAuthentication(router *gin.Engine, service *identity.AuthService, systems []*httpx.SystemAuthentication) {
	identity.RegisterAuthRoutes(router, service, 1)
	for _, system := range systems {
		system.Use(identity.JWTAuth(service))
	}
}

func credentialSealer(cfg *config.Config) (kernelcrypto.Sealer, error) {
	if os.Getenv("CH_CRYPTO_KEYS") != "" || os.Getenv("CH_CRYPTO_ACTIVE_VERSION") != "" {
		return kernelcrypto.FromEnvironment(os.Getenv)
	}
	if cfg == nil || strings.TrimSpace(cfg.Crypto.MasterKey) == "" {
		return nil, nil
	}
	encoded := strings.TrimPrefix(strings.TrimSpace(cfg.Crypto.MasterKey), "base64:")
	key, err := base64.StdEncoding.DecodeString(encoded)
	defer clear(key)
	if err != nil || len(key) != 32 {
		return nil, errors.New("crypto master key is invalid")
	}
	version := cfg.Crypto.KeyVersion
	if version <= 0 {
		return nil, errors.New("crypto key version is invalid")
	}
	return kernelcrypto.NewKeyring(map[int][]byte{version: key}, version)
}

func productCursorSigner(cfg *config.Config) (*catalog.ProductCursorSigner, error) {
	if cfg == nil || strings.TrimSpace(cfg.JWT.Secret) == "" {
		return nil, errors.New("product cursor signing key is required")
	}
	return catalog.NewProductCursorSigner([]byte(cfg.JWT.Secret))
}

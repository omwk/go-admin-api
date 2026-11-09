package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/omwk/go-admin-api/internal/modules/identity"
)

func registerTradeAndGatewayRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, shared *cache.Cache, cfg *config.Config) error {
	if err := registerTradeModules(router, auth, profiles, database, shared, cfg); err != nil {
		return err
	}
	return registerGatewayRoutes(router, auth, profiles, database, cfg)
}

func registerGatewayRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, cfg *config.Config) error {
	sealer, err := credentialSealer(cfg)
	if err != nil {
		return err
	}
	store := &gatewayStore{database: database}
	lifecycle, err := gateway.NewService(store, sealer, time.Now)
	if err != nil {
		return err
	}
	resources, err := gateway.NewResourceService(store)
	if err != nil {
		return err
	}
	return gateway.RegisterAdminRoutes(router, auth, profiles, lifecycle, store, resources, newIdempotencyStore(database))
}

// NewOpenGateway builds a separate HMAC stack and its dedicated nonce pool.
func NewOpenGateway(ctx context.Context, database *db.DB, cfg *config.Config) (http.Handler, func() error, error) {
	if database == nil || cfg == nil {
		return nil, nil, errors.New("open gateway dependencies required")
	}
	sealer, err := credentialSealer(cfg)
	if err != nil {
		return nil, nil, err
	}
	source, err := httpx.NewSourceResolver(cfg.App.TrustedProxies)
	if err != nil {
		return nil, nil, err
	}
	nonces, err := newGatewayNonces(ctx, cfg.Redis)
	if err != nil {
		return nil, nil, err
	}
	store := &gatewayStore{database: database}
	auth, err := gateway.NewAuthenticator(store, nonces, sealer, source, time.Now)
	if err != nil {
		return nil, nil, errors.Join(err, nonces.Close())
	}
	orders, err := NewGatewayOrderReader(database)
	if err != nil {
		return nil, nil, errors.Join(err, nonces.Close())
	}
	calls, err := newGatewayCalls(database, cfg)
	if err != nil {
		return nil, nil, errors.Join(err, nonces.Close())
	}
	limiter, err := newGatewayLimiter(nonces.client, cfg.Gateway)
	if err != nil {
		return nil, nil, errors.Join(err, nonces.Close())
	}
	open, err := gateway.NewOpenHandler(auth, store, orders, calls, limiter, time.Now)
	if err != nil {
		return nil, nil, errors.Join(err, nonces.Close())
	}
	return open, nonces.Close, nil
}

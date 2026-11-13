package bootstrap

import (
	"crypto/hmac"
	"crypto/sha256"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	kernelcrypto "github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/identity"
)

func registerTenantRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, admin *identity.AdminService, database *db.DB, cfg *config.Config) error {
	// Domain separation keeps response encryption independent of JWT signatures.
	derive := hmac.New(sha256.New, []byte(cfg.JWT.Secret))
	if _, err := derive.Write([]byte("identity.tenant.idempotency.v1")); err != nil {
		return err
	}
	key := derive.Sum(nil)
	defer clear(key)
	sealer, err := kernelcrypto.NewKeyring(map[int][]byte{1: key}, 1)
	if err != nil {
		return err
	}
	service, err := identity.NewTenantService(newTenantStore(database), admin, cfg.Tenant.PlatformID, cfg.App.PublicURL, time.Now, sealer)
	if err != nil {
		return err
	}
	return identity.RegisterTenantRoutes(router, auth, profiles, service)
}

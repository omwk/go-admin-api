package bootstrap

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
)

func registerAccountAndAdmin(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, admin *identity.AdminService, database *db.DB, redis *authRedis, cfg *config.Config) error {
	if err := registerAccountLifecycle(router, auth, profiles, admin, database, redis, cfg); err != nil {
		return err
	}
	if err := identity.RegisterAdminRoutes(router, auth, profiles, admin); err != nil {
		return err
	}
	if err := registerTenantRoutes(router, auth, profiles, admin, database, cfg); err != nil {
		return err
	}
	return registerSystemRoutes(router, auth, profiles, admin, database)
}

func registerAccountLifecycle(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, admin *identity.AdminService, database *db.DB, redis *authRedis, cfg *config.Config) error {
	sealer, err := credentialSealer(cfg)
	if err != nil {
		return err
	}
	copyLinks := cfg.App.Env == "dev" || cfg.App.Env == "test"
	notifier := &accountNotifications{manager: newNotificationManager(&notificationStore{database: database}, sealer, time.Now), production: !copyLinks}
	accounts := &identity.AccountService{Store: &accountStore{newAuthStore(database)}, Auth: auth, Admin: admin, Limits: redis, Now: time.Now, Notify: notifier,
		CopyResetLinks: copyLinks, PublicURL: cfg.App.PublicURL, Invalidate: authz.New(redis.cache).Pending}
	auth.UseAccounts(accounts)
	admin.UseAccounts(accounts)
	identity.RegisterAccountSelfRoutes(router, auth, accounts)
	identity.RegisterRecoveryRoutes(router, accounts, 1)
	identity.RegisterAccountRegistrationRoutes(router, accounts)
	return identity.RegisterInviteRoutes(router, auth, profiles, accounts, 1)
}

package bootstrap

import (
	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

func registerCredentialNotificationRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, runtime *credentialRefreshRuntime) error {
	guard, err := identity.RequirePerm(profiles, permission.SystemSettingWrite)
	if err != nil {
		return err
	}
	actor, err := identity.RequireActor(profiles)
	if err != nil {
		return err
	}
	if err := notifier.RegisterRoutes(router, newCredentialNotifications(runtime), notifier.HTTPAccess{
		Authenticate: gin.HandlersChain{identity.JWTAuth(auth), actor}, SettingsPermission: guard,
		Actor: func(c *gin.Context) (int64, int64) { a := identity.AdminActor(c); return a.User.TenantID, a.User.ID },
	}); err != nil {
		return err
	}
	return registerNotificationManagement(router, auth, profiles, runtime)
}

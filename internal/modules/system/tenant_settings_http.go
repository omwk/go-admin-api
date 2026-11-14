package system

import (
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

func tenantSettingAllowed(c *gin.Context) bool {
	a := identity.AdminActor(c)
	return a.User.IsSuper || slices.Contains(a.Permissions, permission.SystemTenantManage) ||
		slices.Contains(a.Permissions, permission.SystemSettingUpdate)
}

func settingPermission(code string) gin.HandlerFunc {
	return func(c *gin.Context) {
		a := identity.AdminActor(c)
		allowed := a.User.IsSuper || slices.Contains(a.Permissions, code)
		if c.Param("group") == "tenant" {
			allowed = tenantSettingAllowed(c)
		}
		if !allowed {
			httpx.Fail(c, errs.New(errs.ErrPermissionDenied, 403, "无权限"))
			return
		}
		c.Next()
	}
}

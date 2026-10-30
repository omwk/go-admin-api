package identity

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
)

// RegisterProfileRoutes adds the two authenticated M1a read endpoints.
func RegisterProfileRoutes(router *gin.Engine, auth *AuthService, profiles *ProfileService) {
	group := router.Group("/api/v1/auth", JWTAuth(auth))
	group.GET("/me", profileHandler(profiles, false))
	group.GET("/menus", profileHandler(profiles, true))
}

func profileHandler(profiles *ProfileService, menus bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.MustGet(claimsKey).(jwt.Claims)
		if !ok {
			httpx.Fail(c, revokedToken())
			return
		}
		user, err := strconv.ParseInt(claims.Subject, 10, 64)
		if err != nil {
			httpx.Fail(c, revokedToken())
			return
		}
		if menus {
			result, err := profiles.Menus(c.Request.Context(), claims.TenantID, user)
			if err != nil {
				httpx.Fail(c, err)
				return
			}
			httpx.OK(c, result)
			return
		}
		result, err := profiles.Me(c.Request.Context(), claims.TenantID, user)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, result)
	}
}

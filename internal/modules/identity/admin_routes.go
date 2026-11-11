package identity

import (
	"errors"
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

const adminPrefix = "/api/v1/admin"
const actorKey = "identity.admin.actor"

// AdminRoute declares one HTTP operation and exactly one registered permission constant.
type AdminRoute struct{ Method, Path, Permission string }

// AdminRoutes returns an owned copy of the complete M1b management route manifest.
func AdminRoutes() []AdminRoute {
	return []AdminRoute{
		{"GET", "/depts", permission.SystemUserList}, {"GET", "/depts/:id", permission.SystemUserList},
		{"GET", "/users/:id", permission.SystemUserList}, {"GET", "/users/:id/roles", permission.SystemUserList},
		{"GET", "/roles/:id", permission.SystemRoleList}, {"GET", "/roles/:id/menus", permission.SystemRoleList},
		{"GET", "/roles/:id/permissions", permission.SystemRoleList}, {"GET", "/roles/:id/depts", permission.SystemRoleList},
		{"GET", "/menus/:id", permission.SystemMenuList},
		{"GET", "/users", permission.SystemUserList}, {"POST", "/users", permission.SystemUserCreate},
		{"PUT", "/users/:id", permission.SystemUserUpdate}, {"DELETE", "/users/:id", permission.SystemUserDelete},
		{"POST", "/users/:id/reset-password", permission.SystemUserReset}, {"PUT", "/users/:id/roles", permission.SystemUserGrant},
		{"GET", "/roles", permission.SystemRoleList}, {"POST", "/roles", permission.SystemRoleCreate},
		{"PUT", "/roles/:id", permission.SystemRoleUpdate}, {"DELETE", "/roles/:id", permission.SystemRoleDelete},
		{"PUT", "/roles/:id/menus", permission.SystemRoleGrant}, {"PUT", "/roles/:id/permissions", permission.SystemRoleGrant},
		{"PUT", "/roles/:id/depts", permission.SystemRoleGrant},
		{"GET", "/menus", permission.SystemMenuList}, {"POST", "/menus", permission.SystemMenuCreate},
		{"PUT", "/menus/:id", permission.SystemMenuUpdate}, {"DELETE", "/menus/:id", permission.SystemMenuDelete},
		{"GET", "/permissions", permission.SystemMenuList},
		{"GET", "/sessions", permission.SystemSessionList}, {"DELETE", "/sessions/:sid", permission.SystemSessionKick},
	}
}

// RequirePerm validates the code at registration and loads current account state on every request.
func RequirePerm(profiles *ProfileService, code string) (gin.HandlerFunc, error) {
	if err := permission.Registered(code); err != nil {
		return nil, err
	}
	if profiles == nil {
		return nil, errors.New("identity profiles are required")
	}
	return func(c *gin.Context) {
		if !loadActor(c, profiles) {
			return
		}
		actor := AdminActor(c)
		if !actor.User.IsSuper && !slices.Contains(actor.Permissions, code) {
			httpx.Fail(c, denied())
			return
		}
		c.Set(actorKey, actor)
		c.Next()
	}, nil
}

// RequireActor authenticates the JWT and loads the current admin identity without
// checking a permission code. It is used by login-only management endpoints.
func RequireActor(profiles *ProfileService) (gin.HandlerFunc, error) {
	if profiles == nil {
		return nil, errors.New("identity profiles are required")
	}
	return func(c *gin.Context) {
		if !loadActor(c, profiles) {
			return
		}
		c.Next()
	}, nil
}

func loadActor(c *gin.Context, profiles *ProfileService) bool {
	claims, ok := c.Get(claimsKey)
	verified, valid := claims.(jwt.Claims)
	if !ok || !valid {
		httpx.Fail(c, revokedToken())
		return false
	}
	user, err := strconv.ParseInt(verified.Subject, 10, 64)
	if err != nil {
		httpx.Fail(c, revokedToken())
		return false
	}
	actor, err := profiles.Me(c.Request.Context(), verified.TenantID, user)
	if err != nil {
		httpx.Fail(c, err)
		return false
	}
	c.Set(actorKey, actor)
	return true
}

// RegisterAdminRoutes binds the management manifest, including scoped session management.
func RegisterAdminRoutes(router *gin.Engine, auth *AuthService, profiles *ProfileService, service *AdminService) error {
	if router == nil || auth == nil || profiles == nil || service == nil {
		return errors.New("management route dependencies are required")
	}
	h := adminHandler{service: service}
	handlers := h.handlers()
	for _, route := range AdminRoutes() {
		if err := permission.Registered(route.Permission); err != nil {
			return err
		}
		if handlers[route.Method+" "+route.Path] == nil {
			return errors.New("management route handler is missing")
		}
	}
	group := router.Group(adminPrefix, JWTAuth(auth))
	for _, route := range AdminRoutes() {
		guard, err := RequirePerm(profiles, route.Permission)
		if err != nil {
			return err
		}
		group.Handle(route.Method, route.Path, guard, handlers[route.Method+" "+route.Path])
	}
	return nil
}

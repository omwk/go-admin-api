package share

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// LinkRoutes is the complete M6b management manifest.
func LinkRoutes() []Route {
	return []Route{{"GET", "/share-links", permission.ShareLinkList}, {"POST", "/share-links", permission.ShareLinkCreate},
		{"POST", "/share-links/:id/regenerate", permission.ShareLinkUpdate}, {"POST", "/share-links/:id/revoke", permission.ShareLinkUpdate}}
}

// RegisterLinkRoutes binds every publication operation to live JWT, permission and owner scope.
func RegisterLinkRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, svc *LinkService) error {
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	for _, route := range LinkRoutes() {
		guard, err := identity.RequirePerm(profiles, route.Permission)
		if err != nil {
			return err
		}
		group.Handle(route.Method, route.Path, guard, linkHandler(svc, route))
	}
	return nil
}

func linkHandler(s *LinkService, route Route) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		user := identity.AdminActor(c).User
		a := Actor{TenantID: user.TenantID, UserID: user.ID}
		ctx, err := s.base.Authorize(c.Request.Context(), a, route.Permission)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		c.Request = c.Request.WithContext(ctx)
		if route.Method == "GET" {
			var q Query
			if c.ShouldBindQuery(&q) != nil {
				httpx.Fail(c, Invalid())
				return
			}
			out, err := s.Links(ctx, a, q)
			if err != nil {
				httpx.Fail(c, err)
				return
			}
			httpx.OK(c, out)
			return
		}
		if err := linkWrite(c, s, a, route); err != nil {
			httpx.Fail(c, err)
		}
	}
}

func linkWrite(c *gin.Context, s *LinkService, a Actor, route Route) error {
	id := int64(0)
	if c.Param("id") != "" {
		var err error
		id, err = strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			return Invalid()
		}
	}
	input, enabled, err := httpx.ExtractIdempotency(c)
	if err != nil {
		return err
	}
	var op *idem.Operation
	if enabled {
		prepared, err := idem.Prepare(a.TenantID, a.UserID, input)
		if err != nil {
			return Invalid()
		}
		op = &prepared
	}
	if route.Path == "/share-links/:id/revoke" {
		if err := s.Revoke(c.Request.Context(), a, id, op); err != nil {
			return err
		}
		httpx.NoContent(c)
		return nil
	}
	var in LinkInput
	if err := body(c, &in); err != nil {
		return err
	}
	out, err := s.Issue(c.Request.Context(), a, id, in, op)
	if err != nil {
		return err
	}
	httpx.Created(c, out)
	return nil
}

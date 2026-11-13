package system

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// AuditRoutes declares the two read-only S4 query endpoints.
func AuditRoutes() []Route {
	return []Route{
		{"GET", "/admin/audit-logs", permission.SystemAuditList},
		{"GET", "/admin/login-logs", permission.SystemAuditList},
	}
}

// RegisterAuditRoutes attaches audit queries to the current identity guards.
func RegisterAuditRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *AuditService) error {
	if router == nil || auth == nil || profiles == nil || service == nil {
		return errors.New("audit HTTP dependencies are required")
	}
	group := router.Group("/api/v1", identity.JWTAuth(auth))
	for _, route := range AuditRoutes() {
		guard, err := identity.RequirePerm(profiles, route.Permission)
		if err != nil {
			return err
		}
		group.Handle(route.Method, route.Path, guard, auditHandler(service, route.Path == "/admin/login-logs"))
	}
	return nil
}

func auditHandler(service *AuditService, login bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		filter, err := auditHTTPFilter(c, login)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		if login {
			value, err := service.LoginLogs(c.Request.Context(), actor(c), filter)
			respond(c, value, err)
		} else {
			value, err := service.AuditLogs(c.Request.Context(), actor(c), filter)
			respond(c, value, err)
		}
	}
}

func auditHTTPFilter(c *gin.Context, login bool) (AuditFilter, error) {
	filter := AuditFilter{Page: 1, PageSize: 20, Result: c.Query("result")}
	for key, target := range map[string]*int{"page": &filter.Page, "page_size": &filter.PageSize} {
		if raw, exists := c.GetQuery(key); exists {
			number, err := strconv.Atoi(raw)
			if err != nil {
				return filter, invalidHTTP()
			}
			*target = number
		}
	}
	idKey := "actor_id"
	if login {
		idKey, filter.Action = "user_id", "identity.login"
	} else {
		filter.Action, filter.ResourceType = c.Query("action"), c.Query("resource_type")
	}
	if raw := c.Query(idKey); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return filter, invalidHTTP()
		}
		if login {
			filter.ResourceID = strconv.FormatInt(id, 10)
		} else {
			filter.ActorID = id
		}
	}
	for key, target := range map[string]**time.Time{"from": &filter.From, "to": &filter.To} {
		if raw := c.Query(key); raw != "" {
			parsed, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return filter, invalidHTTP()
			}
			utc := parsed.UTC()
			*target = &utc
		}
	}
	return filter, validateAuditFilter(actor(c), filter)
}

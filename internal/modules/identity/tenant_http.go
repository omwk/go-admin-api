package identity

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// TenantRoutes is the complete S5 route and permission manifest.
func TenantRoutes() []AdminRoute {
	return []AdminRoute{
		{"GET", "/tenant-templates", permission.SystemTenantManage},
		{"POST", "/tenant-templates", permission.SystemTenantManage},
		{"GET", "/tenant-templates/:id", permission.SystemTenantManage},
		{"PUT", "/tenant-templates/:id", permission.SystemTenantManage},
		{"DELETE", "/tenant-templates/:id", permission.SystemTenantManage},
		{"GET", "/tenants", permission.SystemTenantList},
		{"GET", "/tenants/:id", permission.SystemTenantList},
		{"POST", "/tenants", permission.SystemTenantManage},
		{"PUT", "/tenants/:id", permission.SystemTenantManage},
		{"POST", "/tenants/:id/disable", permission.SystemTenantManage},
		{"POST", "/tenants/:id/enable", permission.SystemTenantManage},
	}
}

// RegisterTenantRoutes reuses live identity checks and the management transaction boundary.
func RegisterTenantRoutes(router *gin.Engine, auth *AuthService, profiles *ProfileService, service *TenantService) error {
	if router == nil || auth == nil || profiles == nil || service == nil {
		return errors.New("tenant route dependencies are required")
	}
	h := tenantHandler{service: service}
	handlers := h.handlers()
	group := router.Group(adminPrefix, JWTAuth(auth))
	for _, route := range TenantRoutes() {
		guard, err := RequirePerm(profiles, route.Permission)
		if err != nil {
			return err
		}
		group.Handle(route.Method, route.Path, guard, handlers[route.Method+" "+route.Path])
	}
	return nil
}

type tenantHandler struct{ service *TenantService }

func tenantOperation[T any](h tenantHandler, c *gin.Context, run func(context.Context, Overview, int64, T) (any, int, error)) {
	c.Header("Cache-Control", "no-store")
	id, err := adminID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	request, body, err := adminDecode[T](c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if len(key) > 256 {
		httpx.Fail(c, invalidAdmin())
		return
	}
	if key != "" {
		key = adminDigest([]byte(key))
	}
	actor := AdminActor(c)
	scope := strconv.FormatInt(actor.User.ID, 10) + ":" + c.Request.Method + ":" + c.Request.URL.Path
	provision := c.Request.Method == "POST" && c.Request.URL.Path == adminPrefix+"/tenants"
	response, err := h.service.execute(c.Request.Context(), actor, scope, key, adminDigest(body), provision, func(ctx context.Context) (repo.SavedResponse, error) {
		value, status, err := run(ctx, actor, id, request)
		if err != nil {
			return repo.SavedResponse{}, err
		}
		data, err := json.Marshal(value)
		return repo.SavedResponse{Status: status, Data: data}, err
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	switch response.Status {
	case 201:
		httpx.Created(c, response.Data)
	case 204:
		httpx.NoContent(c)
	default:
		httpx.OK(c, response.Data)
	}
}

func tenantPage(c *gin.Context) (repo.Page, error) {
	page, err := adminPagination(c, false)
	if err != nil || page.Sort != "-created_at" {
		return page, invalidAdmin()
	}
	return page, nil
}

package catalog

import (
	"encoding/json"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// ExportRoutes lists the two asynchronous export API contracts.
func ExportRoutes() []CategoryRoute {
	return []CategoryRoute{{"POST", "/products/export", permission.ProductSPUExport}, {"GET", "/exports/:id", permission.ProductSPUExport}}
}

// RegisterExportRoutes installs guarded creation and owner-scoped downloads.
func RegisterExportRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *ExportService) error {
	if router == nil || auth == nil || profiles == nil || service == nil {
		return errors.New("export route dependencies required")
	}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	for _, route := range ExportRoutes() {
		guard, err := identity.RequirePerm(profiles, route.Permission)
		if err != nil {
			return err
		}
		group.Handle(route.Method, route.Path, guard, exportHandler(service, route.Method))
	}
	return nil
}

func exportHandler(service *ExportService, method string) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor := identity.AdminActor(c)
		tenant, user := actor.User.TenantID, actor.User.ID
		if method == "GET" {
			id, err := productPathID(c)
			if err != nil {
				httpx.Fail(c, err)
				return
			}
			result, err := service.Status(c.Request.Context(), tenant, user, id)
			if err != nil {
				httpx.Fail(c, err)
				return
			}
			httpx.OK(c, result)
			return
		}
		var input ExportRequest
		if err := productBody(c, &input); err != nil {
			httpx.Fail(c, err)
			return
		}
		var operation *idem.Operation
		if key := c.GetHeader("Idempotency-Key"); key != "" {
			body, err := json.Marshal(input)
			if err != nil {
				httpx.Fail(c, err)
				return
			}
			op, err := idem.Prepare(tenant, user, idem.Input{Key: key, Method: "POST", Route: "/api/v1/admin/products/export", Body: body})
			if err != nil {
				httpx.Fail(c, invalidProduct())
				return
			}
			operation = &op
		}
		job, err := service.Create(c.Request.Context(), tenant, user, input, operation)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.Created(c, job)
	}
}

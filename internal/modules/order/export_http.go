package order

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// RegisterExportRoutes installs the order-specific creation and download contracts.
func RegisterExportRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *ExportService) error {
	if router == nil || auth == nil || profiles == nil || service == nil {
		return errors.New("order export route dependencies required")
	}
	guard, err := identity.RequirePerm(profiles, permission.TradeOrderExport)
	if err != nil {
		return err
	}
	group := router.Group("/api/v1/admin/orders", identity.JWTAuth(auth), guard)
	group.POST("/export", createExportHandler(service))
	group.GET("/exports/:id", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			httpx.Fail(c, invalidQuery())
			return
		}
		actor := identity.AdminActor(c)
		status, err := service.Status(c.Request.Context(), actor.User.TenantID, actor.User.ID, id)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, status)
	})
	return nil
}

func createExportHandler(service *ExportService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input ExportRequest
		decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			httpx.Fail(c, invalidQuery())
			return
		}
		actor := identity.AdminActor(c)
		tenant, user := actor.User.TenantID, actor.User.ID
		var op *idem.Operation
		if key := c.GetHeader("Idempotency-Key"); key != "" {
			body, err := json.Marshal(input)
			if err != nil {
				httpx.Fail(c, invalidQuery())
				return
			}
			operation, err := idem.Prepare(tenant, user, idem.Input{Key: key, Method: "POST", Route: "/api/v1/admin/orders/export", Body: body})
			if err != nil {
				httpx.Fail(c, invalidQuery())
				return
			}
			op = &operation
		}
		job, err := service.Create(c.Request.Context(), tenant, user, input, op)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.Created(c, job)
	}
}

package order

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// List returns owner-bound order history filtered by the current shop scope.
func (s *ExportService) List(ctx context.Context, tenant, user int64, query catalog.ExportListQuery) (catalog.ExportPage, error) {
	ctx, err := s.authorize(ctx, tenant, user, permission.TradeOrderExport)
	if err != nil {
		return catalog.ExportPage{}, err
	}
	return s.store.ListOrderExports(ctx, tenant, user, query)
}

func orderExportListHandler(service *ExportService) gin.HandlerFunc {
	return func(c *gin.Context) {
		query, err := catalog.ParseExportListQuery(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		actor := identity.AdminActor(c)
		result, err := service.List(c.Request.Context(), actor.User.TenantID, actor.User.ID, query)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, result)
	}
}

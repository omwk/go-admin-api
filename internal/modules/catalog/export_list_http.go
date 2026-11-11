package catalog

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity"
)

// ParseExportListQuery validates pagination shared by product and order history.
func ParseExportListQuery(c *gin.Context) (ExportListQuery, error) {
	query := ExportListQuery{Page: 1, PageSize: 20}
	for name, target := range map[string]*int{"page": &query.Page, "page_size": &query.PageSize} {
		if raw, ok := c.GetQuery(name); ok {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 {
				return query, invalidProduct()
			}
			*target = value
		}
	}
	query.PageSize = min(query.PageSize, 100)
	if query.Page > 10000 || (query.Page-1)*query.PageSize >= 10000 {
		return query, invalidProduct()
	}
	if sort, exists := c.GetQuery("sort"); exists && sort != "-created_at" {
		return query, invalidProduct()
	}
	return query, nil
}

func exportListHandler(service *ExportService) gin.HandlerFunc {
	return func(c *gin.Context) {
		query, err := ParseExportListQuery(c)
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

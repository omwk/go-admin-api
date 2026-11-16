package identity

import (
	"context"

	"github.com/gin-gonic/gin"
)

func (h tenantHandler) archive(restore bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantOperation(h, c, func(ctx context.Context, a Overview, id int64, _ noAdminBody) (any, int, error) {
			if restore {
				row, err := h.service.RestoreTenant(ctx, a, id)
				return row, 200, err
			}
			row, err := h.service.ArchiveTenant(ctx, a, id)
			return row, 200, err
		})
	}
}

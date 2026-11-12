package identity

import (
	"context"

	"github.com/gin-gonic/gin"
)

func (h adminHandler) createDepartment(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, actor Overview, _ int64, request DepartmentRequest) (any, int, error) {
		row, err := h.service.CreateDepartment(ctx, actor, request)
		return row, 201, err
	})
}

func (h adminHandler) updateDepartment(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, actor Overview, id int64, request DepartmentRequest) (any, int, error) {
		row, err := h.service.UpdateDepartment(ctx, actor, id, request)
		return row, 200, err
	})
}

func (h adminHandler) deleteDepartment(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, actor Overview, id int64, _ noAdminBody) (any, int, error) {
		return nil, 204, h.service.DeleteDepartment(ctx, actor, id)
	})
}

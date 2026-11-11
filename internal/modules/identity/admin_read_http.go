package identity

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

func adminRead[T any](c *gin.Context, run func(context.Context, Overview, int64) (T, error)) {
	id, err := adminID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	value, err := run(c.Request.Context(), adminActor(c), id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, value)
}

func (h adminHandler) readUser(c *gin.Context)   { adminRead(c, h.service.User) }
func (h adminHandler) readRole(c *gin.Context)   { adminRead(c, h.service.Role) }
func (h adminHandler) readMenu(c *gin.Context)   { adminRead(c, h.service.Menu) }
func (h adminHandler) department(c *gin.Context) { adminRead(c, h.service.Department) }

func (h adminHandler) departments(c *gin.Context) {
	adminRead(c, func(ctx context.Context, actor Overview, _ int64) ([]*AdminDepartment, error) {
		return h.service.Departments(ctx, actor)
	})
}

func (h adminHandler) readLinks(kind repo.LinkKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		adminRead(c, func(ctx context.Context, actor Overview, id int64) ([]int64, error) {
			return h.service.Links(ctx, actor, kind, id)
		})
	}
}

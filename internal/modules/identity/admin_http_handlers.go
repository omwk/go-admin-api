package identity

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

func (h adminHandler) handlers() map[string]gin.HandlerFunc {
	return map[string]gin.HandlerFunc{
		"GET /users": h.users, "POST /users": h.createUser, "PUT /users/:id": h.updateUser, "DELETE /users/:id": h.deleteUser,
		"POST /users/:id/reset-password": h.resetPassword, "PUT /users/:id/roles": h.userRoles,
		"GET /roles": h.roles, "POST /roles": h.createRole, "PUT /roles/:id": h.updateRole, "DELETE /roles/:id": h.deleteRole,
		"PUT /roles/:id/menus": h.roleMenus, "PUT /roles/:id/permissions": h.rolePermissions, "PUT /roles/:id/depts": h.roleDepartments,
		"GET /menus": h.menus, "POST /menus": h.createMenu, "PUT /menus/:id": h.updateMenu, "DELETE /menus/:id": h.deleteMenu,
		"GET /permissions": h.permissions,
		"GET /sessions":    h.sessions, "DELETE /sessions/:sid": h.kick,
	}
}

func (h adminHandler) users(c *gin.Context) {
	page, err := adminPagination(c, false)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.service.Users(c.Request.Context(), adminActor(c), page)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}
func (h adminHandler) roles(c *gin.Context) {
	page, err := adminPagination(c, true)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.service.Roles(c.Request.Context(), adminActor(c), page)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}
func (h adminHandler) menus(c *gin.Context) {
	result, err := h.service.Menus(c.Request.Context(), adminActor(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}
func (h adminHandler) permissions(c *gin.Context) {
	result, err := h.service.Permissions(c.Request.Context(), adminActor(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}

func (h adminHandler) createUser(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, a Overview, _ int64, r CreateUserRequest) (any, int, error) {
		v, e := h.service.CreateUser(ctx, a, r)
		return v, 201, e
	})
}
func (h adminHandler) updateUser(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, a Overview, id int64, r UserFields) (any, int, error) {
		v, e := h.service.UpdateUser(ctx, a, id, r)
		return v, 200, e
	})
}
func (h adminHandler) deleteUser(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, a Overview, id int64, _ noAdminBody) (any, int, error) {
		return nil, 204, h.service.DeleteUser(ctx, a, id)
	})
}
func (h adminHandler) resetPassword(c *gin.Context) {
	type request struct {
		Password string `json:"password" binding:"required,min=8,max=72"`
	}
	adminWrite(h, c, func(ctx context.Context, a Overview, id int64, r request) (any, int, error) {
		return nil, 204, h.service.ResetPassword(ctx, a, id, r.Password)
	})
}
func (h adminHandler) createRole(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, a Overview, _ int64, r RoleRequest) (any, int, error) {
		v, e := h.service.CreateRole(ctx, a, r)
		return v, 201, e
	})
}
func (h adminHandler) updateRole(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, a Overview, id int64, r RoleRequest) (any, int, error) {
		v, e := h.service.UpdateRole(ctx, a, id, r)
		return v, 200, e
	})
}
func (h adminHandler) deleteRole(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, a Overview, id int64, _ noAdminBody) (any, int, error) {
		return nil, 204, h.service.DeleteRole(ctx, a, id)
	})
}
func (h adminHandler) createMenu(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, a Overview, _ int64, r MenuRequest) (any, int, error) {
		v, e := h.service.CreateMenu(ctx, a, r)
		return v, 201, e
	})
}
func (h adminHandler) updateMenu(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, a Overview, id int64, r MenuRequest) (any, int, error) {
		v, e := h.service.UpdateMenu(ctx, a, id, r)
		return v, 200, e
	})
}
func (h adminHandler) deleteMenu(c *gin.Context) {
	adminWrite(h, c, func(ctx context.Context, a Overview, id int64, _ noAdminBody) (any, int, error) {
		return nil, 204, h.service.DeleteMenu(ctx, a, id)
	})
}
func (h adminHandler) userRoles(c *gin.Context) {
	type request struct {
		IDs []int64 `json:"role_ids" binding:"required"`
	}
	adminWrite(h, c, func(ctx context.Context, a Overview, id int64, r request) (any, int, error) {
		return nil, 204, h.service.Grant(ctx, a, repo.UserRoles, id, r.IDs)
	})
}
func (h adminHandler) roleMenus(c *gin.Context) {
	type request struct {
		IDs []int64 `json:"menu_ids" binding:"required"`
	}
	adminWrite(h, c, func(ctx context.Context, a Overview, id int64, r request) (any, int, error) {
		return nil, 204, h.service.Grant(ctx, a, repo.RoleMenus, id, r.IDs)
	})
}
func (h adminHandler) rolePermissions(c *gin.Context) {
	type request struct {
		IDs []int64 `json:"permission_ids" binding:"required"`
	}
	adminWrite(h, c, func(ctx context.Context, a Overview, id int64, r request) (any, int, error) {
		return nil, 204, h.service.Grant(ctx, a, repo.RolePermissions, id, r.IDs)
	})
}
func (h adminHandler) roleDepartments(c *gin.Context) {
	type request struct {
		IDs []int64 `json:"dept_ids" binding:"required"`
	}
	adminWrite(h, c, func(ctx context.Context, a Overview, id int64, r request) (any, int, error) {
		return nil, 204, h.service.Grant(ctx, a, repo.RoleDepartments, id, r.IDs)
	})
}

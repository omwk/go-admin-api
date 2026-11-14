package identity

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

type tenantName struct {
	Name string `json:"name" binding:"required,max=128"`
}

func (h tenantHandler) handlers() map[string]gin.HandlerFunc {
	return map[string]gin.HandlerFunc{
		"GET /tenants":                   h.tenants,
		"GET /tenant-templates":          h.templates,
		"GET /tenants/:id/members":       h.members,
		"POST /tenants/:id/admin-invite": h.adminInvite,
		"GET /tenants/:id": func(c *gin.Context) {
			tenantOperation(h, c, func(ctx context.Context, a Overview, id int64, _ noAdminBody) (any, int, error) {
				row, err := h.service.Tenant(ctx, a, id)
				return row, 200, err
			})
		},
		"POST /tenants": func(c *gin.Context) {
			tenantOperation(h, c, func(ctx context.Context, a Overview, _ int64, in ProvisionTenantInput) (any, int, error) {
				row, err := h.service.Provision(ctx, a, in)
				return row, 201, err
			})
		},
		"PUT /tenants/:id": func(c *gin.Context) {
			tenantOperation(h, c, func(ctx context.Context, a Overview, id int64, in tenantName) (any, int, error) {
				row, err := h.service.RenameTenant(ctx, a, id, in.Name)
				return row, 200, err
			})
		},
		"POST /tenants/:id/disable": h.enabled(false),
		"POST /tenants/:id/enable":  h.enabled(true),
		"GET /tenant-templates/:id": func(c *gin.Context) {
			tenantOperation(h, c, func(ctx context.Context, a Overview, id int64, _ noAdminBody) (any, int, error) {
				row, err := h.service.Template(ctx, a, id)
				return row, 200, err
			})
		},
		"POST /tenant-templates": func(c *gin.Context) {
			tenantOperation(h, c, func(ctx context.Context, a Overview, _ int64, in CreateTemplateInput) (any, int, error) {
				row, err := h.service.CreateTemplate(ctx, a, in)
				return row, 201, err
			})
		},
		"PUT /tenant-templates/:id": func(c *gin.Context) {
			tenantOperation(h, c, func(ctx context.Context, a Overview, id int64, in TemplateFields) (any, int, error) {
				row, err := h.service.UpdateTemplate(ctx, a, id, in)
				return row, 200, err
			})
		},
		"DELETE /tenant-templates/:id": func(c *gin.Context) {
			tenantOperation(h, c, func(ctx context.Context, a Overview, id int64, _ noAdminBody) (any, int, error) {
				return nil, 204, h.service.DeleteTemplate(ctx, a, id)
			})
		},
	}
}

func (h tenantHandler) adminInvite(c *gin.Context) {
	if err := h.service.global(AdminActor(c)); err != nil {
		httpx.Fail(c, err)
		return
	}
	force, err := strconv.ParseBool(c.DefaultQuery("force", "false"))
	if err != nil {
		httpx.Fail(c, invalidAdmin())
		return
	}
	tenantOperation(h, c, func(ctx context.Context, a Overview, id int64, _ noAdminBody) (any, int, error) {
		row, err := h.service.ReissueAdminInvite(ctx, a, id, force)
		return row, 201, err
	})
}

func (h tenantHandler) members(c *gin.Context) {
	if err := h.service.global(AdminActor(c)); err != nil {
		httpx.Fail(c, err)
		return
	}
	page, err := tenantPage(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	tenantOperation(h, c, func(ctx context.Context, a Overview, id int64, _ noAdminBody) (any, int, error) {
		row, err := h.service.TenantMembers(ctx, a, id, page)
		return row, 200, err
	})
}

func (h tenantHandler) enabled(value bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantOperation(h, c, func(ctx context.Context, a Overview, id int64, _ noAdminBody) (any, int, error) {
			row, err := h.service.SetTenantEnabled(ctx, a, id, value)
			return row, 200, err
		})
	}
}

func (h tenantHandler) tenants(c *gin.Context) {
	page, err := tenantPage(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	tenantOperation(h, c, func(ctx context.Context, a Overview, _ int64, _ noAdminBody) (any, int, error) {
		row, err := h.service.Tenants(ctx, a, page)
		return row, 200, err
	})
}

func (h tenantHandler) templates(c *gin.Context) {
	page, err := tenantPage(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	tenantOperation(h, c, func(ctx context.Context, a Overview, _ int64, _ noAdminBody) (any, int, error) {
		row, err := h.service.Templates(ctx, a, page)
		return row, 200, err
	})
}

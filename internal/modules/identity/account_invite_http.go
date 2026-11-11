package identity

import (
	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// RegisterInviteRoutes binds invitation management and anonymous acceptance.
func RegisterInviteRoutes(router *gin.Engine, auth *AuthService, profiles *ProfileService, service *AccountService, tenant int64) error {
	h := accountHandler{service: service, tenant: tenant}
	group := router.Group("/api/v1/admin", JWTAuth(auth))
	for _, route := range []struct {
		method, path, permission string
		handler                  gin.HandlerFunc
	}{
		{"POST", "/invites", permission.SystemUserCreate, h.createInvite},
		{"GET", "/invites", permission.SystemUserList, h.invites},
		{"DELETE", "/invites/:id", permission.SystemUserDelete, h.revokeInvite},
		{"POST", "/users/:id/reset-link", permission.SystemUserReset, h.resetLink},
	} {
		guard, err := RequirePerm(profiles, route.permission)
		if err != nil {
			return err
		}
		group.Handle(route.method, route.path, guard, route.handler)
	}
	router.POST("/api/v1/auth/invite/accept", h.acceptInvite)
	return nil
}

func (h accountHandler) createInvite(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	in, _, err := adminDecode[InviteInput](c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	out, err := h.service.CreateInvite(c.Request.Context(), AdminActor(c), in)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, out)
}

func (h accountHandler) invites(c *gin.Context) {
	page, err := adminPagination(c, false)
	if err != nil || page.Sort != "-created_at" {
		httpx.Fail(c, invalidAdmin())
		return
	}
	out, err := h.service.Invites(c.Request.Context(), AdminActor(c), page)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h accountHandler) revokeInvite(c *gin.Context) {
	id, err := adminID(c)
	if err == nil {
		err = h.service.RevokeInvite(c.Request.Context(), AdminActor(c), id)
	}
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h accountHandler) acceptInvite(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var in struct {
		AcceptInviteInput
		TenantID int64 `json:"tenant_id" binding:"gte=0"`
	}
	if c.ShouldBindJSON(&in) != nil {
		httpx.Fail(c, invalidLink())
		return
	}
	tenant := in.TenantID
	if tenant == 0 {
		tenant = h.tenant
	}
	out, err := h.service.AcceptInvite(c.Request.Context(), tenant, in.AcceptInviteInput, requestMeta(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, out)
}

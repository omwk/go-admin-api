package identity

import (
	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

// RegisterAccountSelfRoutes installs the authenticated lifecycle endpoints.
func RegisterAccountSelfRoutes(router *gin.Engine, auth *AuthService, service *AccountService) {
	h := accountHandler{service: service}
	group := router.Group("/api/v1/auth", JWTAuth(auth))
	group.PUT("/password", h.password)
	group.PUT("/profile", h.profile)
	group.GET("/sessions", h.sessions)
	group.DELETE("/sessions/:sid", h.deleteSession)
	group.POST("/logout-all", h.logoutAll)
}

type accountHandler struct {
	service *AccountService
	tenant  int64
}

func (h accountHandler) password(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var in struct {
		OldPassword string `json:"oldPassword" binding:"required,max=1024"`
		NewPassword string `json:"newPassword" binding:"required,max=72"`
	}
	if c.ShouldBindJSON(&in) != nil {
		httpx.Fail(c, invalidAdmin())
		return
	}
	if err := h.service.ChangePassword(c.Request.Context(), AuthClaims(c), in.OldPassword, in.NewPassword); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h accountHandler) profile(c *gin.Context) {
	var in ProfileInput
	if c.ShouldBindJSON(&in) != nil {
		httpx.Fail(c, invalidAdmin())
		return
	}
	out, pending, err := h.service.UpdateProfile(c.Request.Context(), AuthClaims(c), in)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if pending {
		c.Header("X-Authz-Invalidation", "pending")
	}
	httpx.OK(c, out)
}

func (h accountHandler) sessions(c *gin.Context) {
	page, err := adminPagination(c, false)
	if err != nil || page.Sort != "-created_at" {
		httpx.Fail(c, invalidAdmin())
		return
	}
	out, err := h.service.MySessions(c.Request.Context(), AuthClaims(c), page)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h accountHandler) deleteSession(c *gin.Context) {
	if err := h.service.DeleteSession(c.Request.Context(), AuthClaims(c), c.Param("sid")); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h accountHandler) logoutAll(c *gin.Context) {
	if err := h.service.LogoutAll(c.Request.Context(), AuthClaims(c)); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

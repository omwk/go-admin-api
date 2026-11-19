package identity

import (
	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

func (h accountHandler) emailChange(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	in, _, err := adminDecode[struct {
		Email string `json:"email" binding:"required,max=256"`
	}](c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	out, err := h.service.ChangeEmail(c.Request.Context(), AuthClaims(c), in.Email, requestMeta(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := httpx.Serialize(c, 202, out)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.WriteResult(c, result)
}

func (h accountHandler) emailVerify(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	in, _, err := adminDecode[struct {
		Token string `json:"token" binding:"required,max=128"`
	}](c)
	if err != nil {
		httpx.Fail(c, invalidLink())
		return
	}
	out, pending, err := h.service.VerifyEmailChange(c.Request.Context(), in.Token, requestMeta(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if pending {
		c.Header("X-Authz-Invalidation", "pending")
	}
	httpx.OK(c, out)
}

package identity

import (
	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

// RegisterAccountRegistrationRoutes binds anonymous registration and email verification.
func RegisterAccountRegistrationRoutes(router *gin.Engine, service *AccountService) {
	h := accountHandler{service: service}
	router.POST("/api/v1/auth/register", h.register)
	router.POST("/api/v1/auth/register/verify", h.verifyRegistration)
}

func (h accountHandler) register(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	in, _, err := adminDecode[RegisterInput](c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	out, err := h.service.Register(c.Request.Context(), in, requestMeta(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := httpx.Serialize(c, out.HTTPStatus, out)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.WriteResult(c, result)
}

func (h accountHandler) verifyRegistration(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	in, _, err := adminDecode[struct {
		Token string `json:"token" binding:"required,max=128"`
	}](c)
	if err != nil {
		httpx.Fail(c, invalidLink())
		return
	}
	if err := h.service.VerifyRegistration(c.Request.Context(), in.Token, requestMeta(c)); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

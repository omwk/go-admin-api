package identity

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
)

const claimsKey = "identity.auth.claims"

// RegisterAuthRoutes installs login, refresh and authenticated logout without T1.4 routes.
func RegisterAuthRoutes(router *gin.Engine, service *AuthService, tenant int64) {
	h := authHandler{service: service, tenant: tenant}
	group := router.Group("/api/v1/auth")
	group.POST("/login", h.login)
	group.POST("/refresh", h.refresh)
	group.POST("/logout", JWTAuth(service), h.logout)
}

type authHandler struct {
	service *AuthService
	tenant  int64
}

func (h authHandler) login(c *gin.Context) {
	var request struct {
		Username string `json:"username" binding:"required,max=256"`
		Password string `json:"password" binding:"required,max=1024"`
	}
	if c.ShouldBindJSON(&request) != nil {
		httpx.Fail(c, errs.New(errs.ErrInvalidParam, 400, "登录参数无效"))
		return
	}
	tokens, err := h.service.Login(c.Request.Context(), h.tenant, request.Username, request.Password, requestMeta(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, tokens)
}

func (h authHandler) refresh(c *gin.Context) {
	var request struct {
		RefreshToken string `json:"refresh_token" binding:"required,max=128"`
	}
	if c.ShouldBindJSON(&request) != nil {
		httpx.Fail(c, errs.New(errs.ErrInvalidParam, 400, "刷新参数无效"))
		return
	}
	tokens, err := h.service.Refresh(c.Request.Context(), request.RefreshToken, requestMeta(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, tokens)
}

func (h authHandler) logout(c *gin.Context) {
	claims, ok := c.MustGet(claimsKey).(jwt.Claims)
	if !ok {
		httpx.Fail(c, revokedToken())
		return
	}
	if err := h.service.Logout(c.Request.Context(), claims); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func requestMeta(c *gin.Context) RequestMeta {
	return RequestMeta{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()}
}

// JWTAuth rejects revoked access and sessions, including revocation facts surviving Redis loss.
func JWTAuth(service *AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			httpx.Fail(c, revokedToken())
			return
		}
		claims, err := service.Authenticate(c.Request.Context(), parts[1])
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		c.Set(claimsKey, claims)
		c.Next()
	}
}

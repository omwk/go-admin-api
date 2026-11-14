package identity

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
)

const claimsKey = "identity.auth.claims"

// RegisterAuthRoutes installs login, refresh, logout and session password reauthentication.
func RegisterAuthRoutes(router *gin.Engine, service *AuthService, defaultCode string) {
	if defaultCode == "" {
		defaultCode = "default"
	}
	h := authHandler{service: service, defaultCode: defaultCode}
	group := router.Group("/api/v1/auth")
	group.POST("/login", h.login)
	group.POST("/refresh", h.refresh)
	group.POST("/logout", JWTAuth(service), h.logout)
	group.POST("/reauth", JWTAuth(service), h.reauth)
}

type authHandler struct {
	service     *AuthService
	defaultCode string
}

func (h authHandler) login(c *gin.Context) {
	var request struct {
		Tenant   string `json:"tenant" binding:"omitempty,max=64"`
		TenantID int64  `json:"tenant_id" binding:"gte=0"`
		Username string `json:"username" binding:"omitempty,max=256"`
		Account  string `json:"account" binding:"omitempty,max=256"`
		Password string `json:"password" binding:"required,max=1024"`
	}
	if c.ShouldBindJSON(&request) != nil {
		httpx.Fail(c, errs.New(errs.ErrInvalidParam, 400, "登录参数无效"))
		return
	}
	account := request.Account
	if account == "" {
		account = request.Username
	}
	code := request.Tenant
	var tokens Tokens
	var err error
	if code == "" && request.TenantID > 0 {
		c.Header("X-Tenant-Deprecated", "1")
		ctx := c.Request.Context()
		logger.From(ctx).WarnContext(ctx, "deprecated tenant_id login parameter", "module", "identity", "action", "identity.login")
		tokens, err = h.service.Login(ctx, request.TenantID, account, request.Password, requestMeta(c))
	} else {
		if code == "" {
			code = h.defaultCode
		}
		tokens, err = h.service.LoginByCode(c.Request.Context(), code, account, request.Password, requestMeta(c))
	}
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
	id := c.GetHeader("X-Request-Id")
	if id == "" {
		id = logger.TraceID(c.Request.Context())
	}
	return boundedRequestMeta(RequestMeta{IP: c.ClientIP(), UserAgent: c.Request.UserAgent(), RequestID: id})
}

func (h authHandler) reauth(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var request struct {
		Password string `json:"password" binding:"required,max=1024"`
	}
	if c.ShouldBindJSON(&request) != nil {
		httpx.Fail(c, errs.New(errs.ErrInvalidParam, 400, "重认证参数无效"))
		return
	}
	result, err := h.service.Reauthenticate(c.Request.Context(), AuthClaims(c), request.Password, requestMeta(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}

// AuthClaims returns claims already verified by JWTAuth, or an empty identity.
func AuthClaims(c *gin.Context) jwt.Claims {
	value, found := c.Get(claimsKey)
	if !found {
		return jwt.Claims{}
	}
	claims, ok := value.(jwt.Claims)
	if !ok {
		return jwt.Claims{}
	}
	return claims
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
		if service.settings != nil {
			c.Set(settingsKey, service.settings)
		}
		if service.accounts != nil {
			user, err := service.accounts.owner(c.Request.Context(), claims)
			if err != nil {
				httpx.Fail(c, err)
				return
			}
			reason, err := service.passwordStatus(c.Request.Context(), user, service.now().UTC())
			if err != nil {
				httpx.Fail(c, err)
				return
			}
			allowed := c.Request.Method == "PUT" && c.Request.URL.Path == "/api/v1/auth/password" || c.Request.Method == "POST" && (c.Request.URL.Path == "/api/v1/auth/logout" || c.Request.URL.Path == "/api/v1/auth/logout-all")
			if reason != "" && !allowed {
				httpx.Fail(c, passwordRequired(reason))
				return
			}
		}
		c.Next()
	}
}

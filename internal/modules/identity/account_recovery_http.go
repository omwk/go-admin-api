package identity

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

// RegisterRecoveryRoutes exposes anonymous recovery under independent abuse budgets.
func RegisterRecoveryRoutes(router *gin.Engine, service *AccountService, tenant int64) {
	h := accountHandler{service: service, tenant: tenant}
	router.POST("/api/v1/auth/password/forgot", h.forgot)
	router.POST("/api/v1/auth/password/reset", h.reset)
}

func (h accountHandler) forgot(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var in struct {
		Account  string `json:"account" binding:"required,max=256"`
		TenantID int64  `json:"tenant_id" binding:"gte=0"`
	}
	if c.ShouldBindJSON(&in) != nil {
		httpx.Fail(c, invalidAdmin())
		return
	}
	tenant := in.TenantID
	if tenant == 0 {
		tenant = h.tenant
	}
	out, err := h.service.ForgotPassword(c.Request.Context(), tenant, in.Account, requestMeta(c))
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

func (h accountHandler) reset(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var in struct {
		Token       string `json:"token" binding:"required,max=128"`
		NewPassword string `json:"newPassword" binding:"required,max=72"`
		TenantID    int64  `json:"tenant_id" binding:"gte=0"`
	}
	if c.ShouldBindJSON(&in) != nil {
		httpx.Fail(c, invalidLink())
		return
	}
	tenant := in.TenantID
	if tenant == 0 {
		tenant = h.tenant
	}
	if err := h.service.ResetPassword(c.Request.Context(), tenant, in.Token, in.NewPassword, requestMeta(c)); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func accountBudget(tenant int64, account string) string {
	return strconv.FormatInt(tenant, 10) + ":" + account
}

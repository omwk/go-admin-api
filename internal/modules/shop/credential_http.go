package shop

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
)

func (h shopHandler) credentials(c *gin.Context) {
	shopID, err := shopID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	page, err := shopPage(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	a := identity.AdminActor(c)
	result, err := h.service.ListCredentials(c.Request.Context(), Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}, shopID, page)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}

func (h shopHandler) createCredential(c *gin.Context) {
	shopID, err := shopID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	input, hasKey, err := httpx.ExtractIdempotency(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var request CredentialCreateRequest
	if err := decodeShop(c, &request); err != nil {
		httpx.Fail(c, err)
		return
	}
	a := identity.AdminActor(c)
	actor := Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}
	if hasKey {
		op, err := idem.Prepare(actor.TenantID, actor.UserID, input)
		if err != nil {
			httpx.Fail(c, invalidShop())
			return
		}
		result, err := h.service.CreateCredentialIdempotent(c.Request.Context(), actor, shopID, request, op, func(status int, data any) (idem.Result, error) {
			return httpx.Serialize(c, status, data)
		})
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.WriteResult(c, result)
		return
	}
	result, err := h.service.CreateCredential(c.Request.Context(), actor, shopID, request)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, result)
}

func (h shopHandler) rotateCredential(c *gin.Context) {
	id, err := credentialID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var request CredentialRotateRequest
	if err := decodeShop(c, &request); err != nil {
		httpx.Fail(c, err)
		return
	}
	a := identity.AdminActor(c)
	result, err := h.service.RotateCredential(c.Request.Context(), Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}, id, request)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}

func (h shopHandler) checkCredential(c *gin.Context) {
	id, err := credentialID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	a := identity.AdminActor(c)
	err = h.service.CheckCredential(c.Request.Context(), Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func (h shopHandler) disableCredential(c *gin.Context) {
	id, err := credentialID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	a := identity.AdminActor(c)
	result, err := h.service.DisableCredential(c.Request.Context(), Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}

func (h shopHandler) credentialAudits(c *gin.Context) {
	id, err := credentialID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	page, err := shopPage(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	a := identity.AdminActor(c)
	result, err := h.service.CredentialAudits(c.Request.Context(), Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}, id, page)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}

func credentialID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("cid"), 10, 64)
	if err != nil || id <= 0 {
		return 0, invalidShop()
	}
	return id, nil
}

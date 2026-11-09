package shop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// RefreshAccepted is the asynchronous receipt; no platform values are exposed.
type RefreshAccepted struct {
	RunID          int64  `json:"run_id"`
	ProtocolStatus string `json:"protocol_status"`
}

// RefreshHistory exposes only normalized control-plane metadata.
type RefreshHistory struct {
	ID              int64      `json:"id"`
	Phase           string     `json:"phase"`
	Status          string     `json:"status"`
	Trigger         string     `json:"trigger"`
	Attempt         int        `json:"attempt"`
	FailureClass    string     `json:"failure_class"`
	StartedAt       time.Time  `json:"started_at"`
	NextRetryAt     *time.Time `json:"next_retry_at"`
	OutputVersionID *int64     `json:"output_version_id"`
}

// RefreshPort is the authorized control-plane entry; it cannot issue a worker capability.
type RefreshPort interface {
	Submit(context.Context, Actor, int64, *idem.Operation) (RefreshAccepted, error)
	History(context.Context, Actor, int64, int, int) (any, error)
}

// RegisterCredentialRefreshRoutes installs asynchronous refresh and safe history routes.
func RegisterCredentialRefreshRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, port RefreshPort) error {
	if router == nil || auth == nil || profiles == nil || port == nil {
		return errors.New("credential refresh HTTP dependencies are required")
	}
	rotate, err := identity.RequirePerm(profiles, permission.ShopCredentialRotate)
	if err != nil {
		return err
	}
	audit, err := identity.RequirePerm(profiles, permission.ShopCredentialAudit)
	if err != nil {
		return err
	}
	group := router.Group("/api/v1/admin/credentials", identity.JWTAuth(auth))
	group.POST("/:cid/refresh", rotate, func(c *gin.Context) {
		if err := validateRefreshBody(c); err != nil {
			httpx.Fail(c, err)
			return
		}
		id, err := credentialID(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		a := identity.AdminActor(c)
		actor := Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}
		input, hasKey, err := httpx.ExtractIdempotency(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		var operation *idem.Operation
		if hasKey {
			prepared, err := idem.Prepare(actor.TenantID, actor.UserID, input)
			if err != nil {
				httpx.Fail(c, err)
				return
			}
			operation = &prepared
		}
		result, err := port.Submit(c.Request.Context(), actor, id, operation)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		response, err := httpx.Serialize(c, 202, result)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.WriteResult(c, response)
	})
	group.GET("/:cid/refresh-runs", audit, func(c *gin.Context) {
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
		result, err := port.History(c.Request.Context(), Actor{TenantID: a.User.TenantID, UserID: a.User.ID}, id, page.Number, page.Size)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, result)
	})
	return nil
}

func validateRefreshBody(c *gin.Context) error {
	if c.Request.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 4097))
	closeErr := c.Request.Body.Close()
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil || closeErr != nil || len(raw) > 4096 {
		return errs.New(10001, 400, "刷新参数无效")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil || input == nil || len(input) > 0 {
		return errs.New(10001, 400, "刷新不接受任务或资源参数")
	}
	return nil
}

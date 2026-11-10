package billing

import (
	"context"
	"errors"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

type statementHandler struct {
	service  *StatementService
	requests idem.Store
}

// RegisterStatementRoutes publishes human-only, owner-scoped financial commands.
func RegisterStatementRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *StatementService, requests idem.Store) error {
	if router == nil || auth == nil || profiles == nil || service == nil || requests == nil {
		return errors.New("statement management dependencies required")
	}
	h := &statementHandler{service: service, requests: requests}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	for _, r := range []struct{ method, path, perm string }{
		{"GET", "/billing-statements", permission.BillingStatementList}, {"GET", "/billing-statements/:id", permission.BillingStatementList},
		{"POST", "/billing-statements:generate", permission.BillingStatementGenerate}, {"POST", "/billing-statements/:id/issue", permission.BillingStatementIssue},
		{"GET", "/billing-adjustments", permission.BillingStatementList}, {"POST", "/billing-adjustments", permission.BillingWalletAdjust},
		{"POST", "/billing-adjustments/:id/apply", permission.BillingWalletAdjust},
	} {
		guard, err := identity.RequirePerm(profiles, r.perm)
		if err != nil {
			return err
		}
		group.Handle(r.method, r.path, guard, h.handle)
	}
	return nil
}

func (h *statementHandler) handle(c *gin.Context) {
	u := identity.AdminActor(c).User
	a := Actor{TenantID: u.TenantID, UserID: u.ID}
	id := int64(0)
	if c.Param("id") != "" {
		var err error
		id, err = strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			httpx.Fail(c, Invalid())
			return
		}
	}
	c.Header("Cache-Control", "private, no-store")
	if c.Request.Method == "GET" {
		h.read(c, a, id)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		httpx.Fail(c, Invalid())
		return
	}
	out, err := h.execute(c.Request.Context(), a, id, c.FullPath(), raw, idem.Input{Key: c.GetHeader("Idempotency-Key"), Method: c.Request.Method, Route: c.FullPath(), PathParams: []string{c.Param("id")}, Body: raw})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, out)
}

func (h *statementHandler) read(c *gin.Context, a Actor, id int64) {
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil {
		httpx.Fail(c, Invalid())
		return
	}
	size, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil {
		httpx.Fail(c, Invalid())
		return
	}
	after, err := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 64)
	if err != nil {
		httpx.Fail(c, Invalid())
		return
	}
	f := StatementFilter{AppID: c.Query("app_id"), Page: page, PageSize: size, After: after}
	var out any
	switch c.FullPath() {
	case "/api/v1/admin/billing-statements":
		out, err = h.service.List(c.Request.Context(), a, f)
	case "/api/v1/admin/billing-adjustments":
		out, err = h.service.Adjustments(c.Request.Context(), a, f)
	default:
		out, err = h.service.Detail(c.Request.Context(), a, id, after, size)
	}
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *statementHandler) execute(ctx context.Context, a Actor, id int64, path string, raw []byte, in idem.Input) (any, error) {
	if in.Key == "" {
		return nil, Invalid()
	}
	op, err := idem.Prepare(a.TenantID, a.UserID, in)
	if err != nil {
		return nil, Invalid()
	}
	var out any
	err = h.service.store.Transaction(ctx, func(tx context.Context) error {
		claim, err := h.requests.Claim(tx, op)
		if err != nil {
			return billingIdemError(err)
		}
		if claim.Action == idem.Replay {
			out = claim.Result.Body
			return nil
		}
		out, err = h.write(tx, a, id, path, raw, in.Key)
		if err != nil {
			return err
		}
		result, err := idem.JSONResult(201, out)
		if err != nil {
			return err
		}
		_, err = h.requests.Complete(tx, op, result)
		return billingIdemError(err)
	})
	return out, err
}

func (h *statementHandler) write(ctx context.Context, a Actor, id int64, path string, raw []byte, key string) (any, error) {
	switch path {
	case "/api/v1/admin/billing-statements:generate":
		var in struct {
			AppID string `json:"app_id"`
			Month string `json:"month"`
		}
		if err := decodeBenefit(raw, &in); err != nil {
			return nil, err
		}
		return h.service.Generate(ctx, a, in.AppID, in.Month)
	case "/api/v1/admin/billing-statements/:id/issue":
		var in struct {
			Version int    `json:"version"`
			Reason  string `json:"reason"`
		}
		if err := decodeBenefit(raw, &in); err != nil {
			return nil, err
		}
		return h.service.Issue(ctx, a, id, in.Version, in.Reason)
	case "/api/v1/admin/billing-adjustments":
		var in AdjustmentInput
		if err := decodeBenefit(raw, &in); err != nil {
			return nil, err
		}
		return h.service.Adjust(ctx, a, in, key)
	default:
		if err := decodeBenefit(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return h.service.ApplyAdjustment(ctx, a, id)
	}
}

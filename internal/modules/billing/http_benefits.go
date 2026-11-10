package billing

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

type benefitHandler struct {
	money    *Service
	reports  *Reconciler
	requests idem.Store
}

func registerBenefitRoutes(group *gin.RouterGroup, profiles *identity.ProfileService, money *Service, requests idem.Store, reports ...*Reconciler) error {
	h := &benefitHandler{money: money, requests: requests}
	if len(reports) > 0 {
		h.reports = reports[0]
	}
	for _, r := range []struct{ method, path, perm string }{
		{"GET", "/pricing-plans", permission.BillingPlanList}, {"POST", "/pricing-plans", permission.BillingPlanCreate}, {"PUT", "/pricing-plans/:id", permission.BillingPlanUpdate},
		{"POST", "/subscriptions", permission.BillingPlanGrant}, {"GET", "/entitlements", permission.BillingPlanList}, {"GET", "/entitlements/:id/txns", permission.BillingPlanList},
		{"GET", "/reconcile-diffs", permission.BillingReconcileList}, {"POST", "/billing-reconciliations", permission.BillingReconcileRun},
		{"POST", "/reconcile-diffs/:id/compensations", permission.BillingReconcileResolve}, {"GET", "/reconcile-diffs/:id/compensations", permission.BillingReconcileList},
		{"GET", "/billing-usage", permission.BillingReconcileList},
	} {
		guard, err := identity.RequirePerm(profiles, r.perm)
		if err != nil {
			return err
		}
		group.Handle(r.method, r.path, guard, h.handle)
	}
	return nil
}

func (h *benefitHandler) handle(c *gin.Context) {
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
	input := idem.Input{Key: c.GetHeader("Idempotency-Key"), Method: c.Request.Method, Route: c.FullPath(), PathParams: []string{c.Param("id")}, Body: raw}
	out, err := h.execute(c.Request.Context(), a, id, c.FullPath(), raw, input)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, out)
}

func (h *benefitHandler) read(c *gin.Context, a Actor, id int64) {
	ctx := c.Request.Context()
	var out any
	var err error
	switch c.FullPath() {
	case "/api/v1/admin/pricing-plans":
		out, err = h.money.Plans(ctx, a)
	case "/api/v1/admin/entitlements":
		out, err = h.money.Entitlements(ctx, a, c.Query("app_id"))
	case "/api/v1/admin/entitlements/:id/txns":
		var rows []UnitTxn
		rows, err = h.money.UnitTxns(ctx, a, id)
		views := make([]unitView, 0, len(rows))
		for _, r := range rows {
			views = append(views, viewUnit(r))
		}
		out = views
	default:
		if h.reports == nil {
			httpx.Fail(c, Unavailable())
			return
		}
		switch c.FullPath() {
		case "/api/v1/admin/reconcile-diffs":
			out, err = h.reports.Differences(ctx, a, c.Query("service_date"))
		case "/api/v1/admin/billing-usage":
			out, err = h.reports.store.DailyUsage(ctx, a, c.Query("service_date"))
		default:
			out, err = h.reports.Compensations(ctx, a, id)
		}
	}
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func decodeBenefit(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(&struct{}{}) != io.EOF {
		return Invalid()
	}
	return nil
}

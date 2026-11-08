package share

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// InquiryRoutes declares management capabilities separately from anonymous intake.
func InquiryRoutes() []Route {
	return []Route{{"GET", "/inquiries", permission.CRMInquiryList}, {"GET", "/inquiries/:id/pii", permission.TradeInquiryPIIRead},
		{"PUT", "/inquiries/:id", permission.CRMInquiryUpdate}, {"GET", "/inquiries/export", permission.TradeInquiryExport}, {"GET", "/share-links/:id/visits", permission.ShareVisitList}}
}

// RegisterInquiryRoutes authenticates every management operation; services audit disclosure denials too.
func RegisterInquiryRoutes(router *gin.Engine, auth *identity.AuthService, svc *InquiryService) {
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	for _, route := range InquiryRoutes() {
		group.Handle(route.Method, route.Path, inquiryAdminHandler(svc, route))
	}
}

func inquiryAdminHandler(s *InquiryService, r Route) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		claims := identity.AuthClaims(c)
		user, err := strconv.ParseInt(claims.Subject, 10, 64)
		if err != nil {
			httpx.Fail(c, Invalid())
			return
		}
		a := Actor{TenantID: claims.TenantID, UserID: user}
		id := int64(0)
		if c.Param("id") != "" {
			id, err = strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil || id <= 0 {
				httpx.Fail(c, Invalid())
				return
			}
		}
		var q Query
		if c.ShouldBindQuery(&q) != nil {
			httpx.Fail(c, Invalid())
			return
		}
		ctx := c.Request.Context()
		var out any
		switch r.Path {
		case "/inquiries":
			out, err = s.List(ctx, a, q)
		case "/inquiries/:id/pii":
			out, err = s.Read(ctx, a, id, claims.SessionID)
		case "/share-links/:id/visits":
			out, err = s.Visits(ctx, a, id, claims.SessionID, q)
		case "/inquiries/export":
			var csv string
			csv, err = s.CSV(ctx, a, q)
			if err == nil {
				c.Header("Content-Disposition", `attachment; filename="inquiries.csv"`)
				c.Data(200, "text/csv; charset=utf-8", []byte(csv))
				return
			}
		default:
			err = inquiryUpdate(c, s, a, id)
			out = struct {
				Updated bool `json:"updated"`
			}{true}
		}
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, out)
	}
}

func inquiryUpdate(c *gin.Context, s *InquiryService, a Actor, id int64) error {
	input, enabled, err := httpx.ExtractIdempotency(c)
	if err != nil {
		return err
	}
	var op *idem.Operation
	if enabled {
		prepared, err := idem.Prepare(a.TenantID, a.UserID, input)
		if err != nil {
			return Invalid()
		}
		op = &prepared
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := body(c, &in); err != nil {
		return err
	}
	return s.Update(c.Request.Context(), a, id, in.Status, op)
}

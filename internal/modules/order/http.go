package order

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// RegisterRoutes installs the three authenticated, non-PII order reads.
func RegisterRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *QueryService, pii ...*PIIService) error {
	if router == nil || auth == nil || profiles == nil || service == nil {
		return errors.New("order route dependencies are required")
	}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	if len(pii) > 0 && pii[0] != nil {
		group.GET("/orders/:id/address", piiHandler(pii[0]))
	}
	for _, route := range []struct{ path, code string }{{"/orders", permission.TradeOrderList}, {"/orders/stats", permission.TradeOrderStats}, {"/orders/:id", permission.TradeOrderRead}} {
		guard, err := identity.RequirePerm(profiles, route.code)
		if err != nil {
			return err
		}
		group.GET(route.path, guard, queryHandler(service, route.path))
	}
	return nil
}

func queryHandler(service *QueryService, path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor := identity.AdminActor(c)
		ctx := c.Request.Context()
		var out any
		var err error
		if path == "/orders/:id" {
			var id int64
			id, err = strconv.ParseInt(c.Param("id"), 10, 64)
			if err == nil {
				out, err = service.Get(ctx, actor.User.TenantID, actor.User.ID, id)
			} else {
				err = invalidQuery()
			}
		} else {
			var query Query
			query, err = parseQuery(c)
			if err == nil && path == "/orders/stats" {
				out, err = service.Stats(ctx, actor.User.TenantID, actor.User.ID, query)
			}
			if err == nil && path == "/orders" {
				out, err = service.List(ctx, actor.User.TenantID, actor.User.ID, query)
			}
		}
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, out)
	}
}

func parseQuery(c *gin.Context) (Query, error) {
	query := Query{Page: 1, PageSize: 20, Status: c.Query("status")}
	for name, target := range map[string]*int{"page": &query.Page, "page_size": &query.PageSize} {
		if raw, ok := c.GetQuery(name); ok {
			v, err := strconv.Atoi(raw)
			if err != nil || v <= 0 {
				return Query{}, invalidQuery()
			}
			*target = v
		}
	}
	if raw, ok := c.GetQuery("shop_id"); ok {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			return Query{}, invalidQuery()
		}
		query.ShopID = v
	}
	for name, target := range map[string]**int64{"min_amount_cents": &query.MinAmountCents, "max_amount_cents": &query.MaxAmountCents} {
		if raw, ok := c.GetQuery(name); ok {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return Query{}, invalidQuery()
			}
			*target = &v
		}
	}
	for name, target := range map[string]**time.Time{"created_from": &query.CreatedFrom, "created_to": &query.CreatedTo} {
		if raw, ok := c.GetQuery(name); ok {
			v, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return Query{}, invalidQuery()
			}
			_, offset := v.Zone()
			if offset != 0 {
				return Query{}, invalidQuery()
			}
			v = v.UTC()
			*target = &v
		}
	}
	if raw, ok := c.GetQuery("include_vanished"); ok {
		if raw != "true" && raw != "false" {
			return Query{}, invalidQuery()
		}
		query.IncludeVanished = raw == "true"
	}
	return normalizeQuery(query)
}

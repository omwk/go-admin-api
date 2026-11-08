package share

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// Route is the complete authenticated pricing HTTP manifest.
type Route struct{ Method, Path, Permission string }

// Routes contains M6a management operations only.
func Routes() []Route {
	return []Route{
		{"GET", "/customers", permission.TradeCustomerList}, {"POST", "/customers", permission.TradeCustomerCreate},
		{"GET", "/customers/:id", permission.TradeCustomerRead}, {"PUT", "/customers/:id", permission.TradeCustomerUpdate}, {"DELETE", "/customers/:id", permission.TradeCustomerDelete},
		{"GET", "/price-lists", permission.TradePriceListList}, {"POST", "/price-lists", permission.TradePriceListCreate},
		{"GET", "/price-lists/:id", permission.TradePriceListRead}, {"PUT", "/price-lists/:id", permission.TradePriceListUpdate}, {"DELETE", "/price-lists/:id", permission.TradePriceListDelete},
		{"POST", "/price-lists/:id/items:batch", permission.TradePriceListWrite}, {"POST", "/price-preview", permission.TradePriceListPreview},
	}
}

// RegisterRoutes installs JWT, permission and live data-scope guards for every route.
func RegisterRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, svc *Service) error {
	if router == nil || auth == nil || profiles == nil || svc == nil {
		return errors.New("pricing route dependencies required")
	}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	for _, route := range Routes() {
		guard, err := identity.RequirePerm(profiles, route.Permission)
		if err != nil {
			return err
		}
		group.Handle(route.Method, route.Path, guard, handler(svc, route))
	}
	return nil
}

func handler(s *Service, r Route) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := identity.AdminActor(c).User
		a := Actor{TenantID: user.TenantID, UserID: user.ID}
		ctx, err := s.Authorize(c.Request.Context(), a, r.Permission)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		c.Request = c.Request.WithContext(ctx)
		id := int64(0)
		if strings.Contains(r.Path, ":id") {
			id, err = strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil || id <= 0 {
				httpx.Fail(c, Invalid())
				return
			}
		}
		if r.Method == "GET" || r.Path == "/price-preview" {
			out, err := read(c, s, a, r, id)
			if err != nil {
				httpx.Fail(c, err)
				return
			}
			httpx.OK(c, out)
			return
		}
		if err := write(c, s, a, r, id); err != nil {
			httpx.Fail(c, err)
		}
	}
}

func read(c *gin.Context, s *Service, a Actor, r Route, id int64) (any, error) {
	ctx := c.Request.Context()
	if r.Path == "/price-preview" {
		var in PreviewInput
		if err := body(c, &in); err != nil {
			return nil, err
		}
		return s.Preview(ctx, a, in)
	}
	if id > 0 {
		if r.Path == "/customers/:id" {
			return s.Customer(ctx, a, id)
		}
		return s.PriceList(ctx, a, id)
	}
	var q Query
	if c.ShouldBindQuery(&q) != nil {
		return nil, Invalid()
	}
	if r.Path == "/customers" {
		return s.Customers(ctx, a, q)
	}
	return s.PriceLists(ctx, a, q)
}

func write(c *gin.Context, s *Service, a Actor, r Route, id int64) error {
	input, enabled, err := httpx.ExtractIdempotency(c)
	if err != nil {
		return err
	}
	var op *idem.Operation
	if enabled {
		if r.Method == "DELETE" {
			input.PathParams = append(input.PathParams, "version="+c.Query("version"))
		}
		prepared, err := idem.Prepare(a.TenantID, a.UserID, input)
		if err != nil {
			return Invalid()
		}
		op = &prepared
	}
	var customer CustomerInput
	var list ListInput
	var batch BatchInput
	version := 0
	switch {
	case r.Method == "DELETE":
		version, err = strconv.Atoi(c.Query("version"))
		if err != nil || version <= 0 {
			return Invalid()
		}
	case strings.HasSuffix(r.Path, "items:batch"):
		err = body(c, &batch)
	case strings.HasPrefix(r.Path, "/customers"):
		err = body(c, &customer)
	default:
		err = body(c, &list)
	}
	if err != nil {
		return err
	}
	check := func(tx context.Context, result idem.Result) error { return s.replayVisible(tx, a, r, id, result) }
	result, err := s.Mutation(c.Request.Context(), a, op, check, func(tx context.Context) (idem.Result, error) {
		var out any
		var err error
		status := 200
		switch {
		case r.Method == "DELETE":
			status = 204
			if r.Path == "/customers/:id" {
				err = s.DeleteCustomer(tx, a, id, version)
			} else {
				err = s.DeletePriceList(tx, a, id, version)
			}
		case strings.HasSuffix(r.Path, "items:batch"):
			out, err = s.BatchItems(tx, a, id, batch)
		case strings.HasPrefix(r.Path, "/customers"):
			out, err = s.SaveCustomer(tx, a, id, customer)
		default:
			out, err = s.SavePriceList(tx, a, id, list)
		}
		if err != nil {
			return idem.Result{}, err
		}
		if r.Method == "POST" && id == 0 {
			status = 201
		}
		return httpx.Serialize(c, status, out)
	})
	if err == nil {
		httpx.WriteResult(c, result)
	}
	return err
}

func body(c *gin.Context, target any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return Invalid()
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Invalid()
	}
	return nil
}

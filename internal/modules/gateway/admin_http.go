package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// AdminRoute binds management operations to the centralized permission registry.
type AdminRoute struct{ Method, Path, Permission string }

// AdminRoutes contains only the M8a lifecycle and publication operations.
func AdminRoutes() []AdminRoute {
	return []AdminRoute{
		{"GET", "/api-clients", permission.GatewayClientList}, {"POST", "/api-clients", permission.GatewayClientCreate},
		{"PUT", "/api-clients/:id", permission.GatewayClientUpdate},
		{"GET", "/api-clients/:id/secrets", permission.GatewayClientList}, {"POST", "/api-clients/:id/secrets", permission.GatewayClientRotate},
		{"DELETE", "/api-clients/:id/secrets/:sid", permission.GatewayClientRotate},
		{"POST", "/api-clients/:id/routes", permission.GatewayClientGrant},
		{"POST", "/api-clients/:id/orders", permission.GatewayClientGrant}, {"DELETE", "/api-clients/:id/orders/:gid", permission.GatewayClientGrant},
		{"GET", "/api-routes", permission.GatewayRouteList}, {"POST", "/api-routes", permission.GatewayRouteCreate}, {"PUT", "/api-routes/:id", permission.GatewayRouteUpdate},
	}
}

// AdminHandler uses existing human JWT/RBAC only on management paths.
type AdminHandler struct {
	lifecycle  *Service
	management ManagementStore
	resources  *ResourceService
	requests   idem.Store
}

// RegisterAdminRoutes installs the management stack without altering human sessions.
func RegisterAdminRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, svc *Service, store ManagementStore, resources *ResourceService, requests idem.Store) error {
	if router == nil || auth == nil || profiles == nil || svc == nil || store == nil || resources == nil || requests == nil {
		return errors.New("gateway management dependencies required")
	}
	h := &AdminHandler{lifecycle: svc, management: store, resources: resources, requests: requests}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	for _, route := range AdminRoutes() {
		guard, err := identity.RequirePerm(profiles, route.Permission)
		if err != nil {
			return err
		}
		group.Handle(route.Method, route.Path, guard, h.handle(route))
	}
	return nil
}

func (h *AdminHandler) handle(route AdminRoute) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := identity.AdminActor(c).User
		a := Actor{TenantID: user.TenantID, UserID: user.ID}
		var id int64
		if c.Param("id") != "" {
			var err error
			id, err = strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil || id <= 0 {
				httpx.Fail(c, Invalid())
				return
			}
		}
		if route.Method == "GET" {
			var out any
			var err error
			switch route.Path {
			case "/api-clients":
				out, err = h.management.ListClients(c.Request.Context(), a)
			case "/api-routes":
				out, err = h.management.ListRoutes(c.Request.Context(), a)
			default:
				out, err = h.management.ListSecrets(c.Request.Context(), a, id)
			}
			if err != nil {
				httpx.Fail(c, err)
				return
			}
			httpx.OK(c, out)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, MaxBodyBytes+1))
		if err != nil || len(raw) > MaxBodyBytes {
			httpx.Fail(c, Invalid())
			return
		}
		var in adminInput
		if len(bytes.TrimSpace(raw)) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&in) != nil || decoder.Decode(&struct{}{}) != io.EOF {
				httpx.Fail(c, Invalid())
				return
			}
		}
		out, status, err := h.execute(c.Request.Context(), a, route, id, c.Param("sid"), c.Param("gid"), in, idem.Input{Key: c.GetHeader("Idempotency-Key"), Method: route.Method, Route: route.Path, PathParams: []string{c.Param("id"), c.Param("sid"), c.Param("gid")}, Body: raw})
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		if status == 201 {
			httpx.Created(c, out)
		} else {
			httpx.OK(c, out)
		}
	}
}

type adminInput struct {
	Name           string     `json:"name"`
	OwnerUserID    int64      `json:"owner_user_id"`
	IPWhitelist    []string   `json:"ip_whitelist"`
	Status         string     `json:"status"`
	Version        int        `json:"version"`
	OverlapSeconds int64      `json:"overlap_seconds"`
	RouteID        int64      `json:"route_id"`
	OrderID        int64      `json:"order_id"`
	ExpiresAt      *time.Time `json:"expires_at"`
	Code           string     `json:"code"`
	Capability     string     `json:"capability"`
	Method         string     `json:"method"`
	Path           string     `json:"path"`
	RouteGroup     string     `json:"route_group"`
	Visibility     string     `json:"visibility"`
	BillingMode    string     `json:"billing_mode"`
	PriceCents     int64      `json:"price_cents"`
	UnitsPerCall   int64      `json:"units_per_call"`
	Currency       string     `json:"currency"`
	QuotaDaily     int64      `json:"quota_daily"`
	QuotaMonthly   int64      `json:"quota_monthly"`
}

func (h *AdminHandler) write(ctx context.Context, a Actor, r AdminRoute, id int64, sid, gid string, in adminInput) (any, int, error) {
	switch r.Path {
	case "/api-clients":
		if in.OwnerUserID == 0 {
			in.OwnerUserID = a.UserID
		}
		c, key, err := h.lifecycle.Create(ctx, a, in.Name, in.OwnerUserID, in.IPWhitelist)
		return map[string]any{"client": ClientView(c), "key": key}, 201, err
	case "/api-clients/:id":
		return nil, 200, h.lifecycle.SetStatus(ctx, a, id, in.Version, in.Status)
	case "/api-clients/:id/secrets":
		if in.OverlapSeconds < 0 || in.OverlapSeconds > 7*24*3600 {
			return nil, 0, Invalid()
		}
		key, err := h.lifecycle.Rotate(ctx, a, id, time.Duration(in.OverlapSeconds)*time.Second)
		return key, 201, err
	case "/api-clients/:id/secrets/:sid":
		key, err := strconv.ParseInt(sid, 10, 64)
		if err != nil {
			return nil, 0, Invalid()
		}
		return nil, 200, h.lifecycle.Revoke(ctx, a, id, key)
	case "/api-clients/:id/routes":
		if in.RouteID <= 0 || in.Status != "active" && in.Status != "suspended" || in.QuotaDaily < 0 || in.QuotaMonthly < 0 || in.QuotaDaily > 2147483647 || in.QuotaMonthly > 2147483647 {
			return nil, 0, Invalid()
		}
		return nil, 200, h.management.GrantRoute(ctx, a, id, in.RouteID, in.Status, in.ExpiresAt, QuotaLimits{Daily: in.QuotaDaily, Monthly: in.QuotaMonthly})
	case "/api-clients/:id/orders":
		out, err := h.resources.Grant(ctx, a, id, in.OrderID)
		return out, 201, err
	case "/api-clients/:id/orders/:gid":
		grant, err := strconv.ParseInt(gid, 10, 64)
		if err != nil {
			return nil, 0, Invalid()
		}
		return nil, 200, h.resources.Revoke(ctx, a, id, grant, in.Version)
	default:
		route := Route{ID: id, Name: in.Name, Code: in.Code, Capability: in.Capability, Method: in.Method, Path: in.Path, RouteGroup: in.RouteGroup, Visibility: in.Visibility, Status: in.Status, BillingMode: in.BillingMode, Version: in.Version, PriceCents: in.PriceCents, UnitsPerCall: in.UnitsPerCall, Currency: in.Currency}
		if err := validateCapability(route); err != nil || len(strings.TrimSpace(in.Name)) == 0 || len(in.Name) > 200 || in.Visibility != "public" && in.Visibility != "private" || in.Status != "enabled" && in.Status != "disabled" || id > 0 && in.Version <= 0 {
			return nil, 0, Invalid()
		}
		out, err := h.management.SaveRoute(ctx, a, route)
		status := 200
		if id == 0 {
			status = 201
		}
		return out, status, err
	}
}

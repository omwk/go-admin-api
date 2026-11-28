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

const (
	infoRoute   = "/gateway/info"
	revealRoute = "/api-clients/:id/secrets/:sid/reveal"
)

// AdminRoutes contains configuration, lifecycle, publication and observation operations.
func AdminRoutes() []AdminRoute {
	return append([]AdminRoute{
		{"GET", infoRoute, permission.GatewayClientList},
		{"GET", "/api-clients", permission.GatewayClientList}, {"POST", "/api-clients", permission.GatewayClientCreate},
		{"GET", "/api-clients/:id", permission.GatewayClientList},
		{"PUT", "/api-clients/:id", permission.GatewayClientUpdate},
		{"GET", "/api-clients/:id/secrets", permission.GatewayClientList}, {"POST", "/api-clients/:id/secrets", permission.GatewayClientRotate},
		{"GET", revealRoute, permission.GatewayClientManage},
		{"DELETE", "/api-clients/:id/secrets/:sid", permission.GatewayClientRotate},
		{"POST", "/api-clients/:id/routes", permission.GatewayClientGrant},
		{"GET", "/api-clients/:id/routes", permission.GatewayClientList},
		{"DELETE", "/api-clients/:id/routes/:grant_id", permission.GatewayClientGrant},
		{"POST", "/api-clients/:id/orders", permission.GatewayClientGrant}, {"DELETE", "/api-clients/:id/orders/:gid", permission.GatewayClientGrant},
		{"GET", "/api-routes", permission.GatewayRouteList}, {"POST", "/api-routes", permission.GatewayRouteCreate}, {"PUT", "/api-routes/:id", permission.GatewayRouteUpdate},
	}, ObservationRoutes()...)
}

// Info exposes the gateway settings needed to display API call examples.
type Info struct {
	PublicBaseURL  string   `json:"public_base_url"`
	AllowedOrigins []string `json:"allowed_origins"`
}

// AdminHandler uses existing human JWT/RBAC only on management paths.
type AdminHandler struct {
	lifecycle   *Service
	management  ManagementStore
	resources   *ResourceService
	requests    idem.Store
	observation *ObservationService
	info        Info
}

// RegisterAdminRoutes installs the management stack without altering human sessions.
func RegisterAdminRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, svc *Service, store ManagementStore, resources *ResourceService, requests idem.Store, info Info, observations ...*ObservationService) error {
	if router == nil || auth == nil || profiles == nil || svc == nil || store == nil || resources == nil || requests == nil {
		return errors.New("gateway management dependencies required")
	}
	info.AllowedOrigins = append([]string{}, info.AllowedOrigins...)
	h := &AdminHandler{lifecycle: svc, management: store, resources: resources, requests: requests, info: info}
	if len(observations) > 0 {
		h.observation = observations[0]
	}
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
		if route.Path == infoRoute {
			httpx.OK(c, h.info)
			return
		}
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
		if route.Path == "/api-calls" || route.Path == "/api-calls/summary" || route.Path == "/api-clients/:id/try" {
			h.observe(c, route, id)
			return
		}
		if route.Path == revealRoute {
			h.revealSecret(c, a, id)
			return
		}
		if route.Method == "GET" {
			var out any
			var err error
			switch route.Path {
			case "/api-clients":
				out, err = h.management.ListClients(c.Request.Context(), a)
			case "/api-clients/:id":
				out, err = h.management.GetClient(c.Request.Context(), a, id)
			case "/api-clients/:id/routes":
				out, err = h.management.ListGrants(c.Request.Context(), a, id)
			case "/api-routes":
				out, err = h.management.ListRoutes(c.Request.Context(), a)
			default:
				out, err = h.lifecycle.listSecrets(c.Request.Context(), a, id)
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
		grantID := c.Param("grant_id")
		if grantID == "" {
			grantID = c.Param("gid")
		}
		out, status, err := h.execute(c.Request.Context(), a, route, id, c.Param("sid"), grantID, in, idem.Input{Key: c.GetHeader("Idempotency-Key"), Method: route.Method, Route: route.Path, PathParams: []string{c.Param("id"), c.Param("sid"), grantID}, Body: raw})
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		if status == 204 {
			httpx.NoContent(c)
		} else if status == 201 {
			httpx.Created(c, out)
		} else {
			httpx.OK(c, out)
		}
	}
}

func (h *AdminHandler) revealSecret(c *gin.Context, actor Actor, clientID int64) {
	c.Header("Cache-Control", "no-store")
	secretID, err := strconv.ParseInt(c.Param("sid"), 10, 64)
	if err != nil || secretID <= 0 {
		httpx.Fail(c, Invalid())
		return
	}
	out, err := h.lifecycle.Reveal(c.Request.Context(), actor, clientID, secretID, c.ClientIP())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, out)
}

type adminInput struct {
	Name           *string    `json:"name"`
	OwnerUserID    int64      `json:"owner_user_id"`
	IPWhitelist    *[]string  `json:"ip_whitelist"`
	Status         *string    `json:"status"`
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
	status := ""
	if in.Status != nil {
		status = *in.Status
	}
	switch r.Path {
	case "/api-clients":
		if in.OwnerUserID == 0 {
			in.OwnerUserID = a.UserID
		}
		name := ""
		if in.Name != nil {
			name = *in.Name
		}
		whitelist := []string{}
		if in.IPWhitelist != nil {
			whitelist = *in.IPWhitelist
		}
		c, key, err := h.lifecycle.Create(ctx, a, name, in.OwnerUserID, whitelist)
		return map[string]any{"client": ClientView(c), "key": key}, 201, err
	case "/api-clients/:id":
		update, err := ValidateClientUpdate(ClientUpdate{Name: in.Name, IPWhitelist: in.IPWhitelist, Status: in.Status}, in.Version)
		if err != nil {
			return nil, 0, err
		}
		out, err := h.management.UpdateClient(ctx, a, id, update, in.Version)
		return out, 200, err
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
		if in.RouteID <= 0 || status != "active" && status != "suspended" || in.QuotaDaily < 0 || in.QuotaMonthly < 0 || in.QuotaDaily > 2147483647 || in.QuotaMonthly > 2147483647 {
			return nil, 0, Invalid()
		}
		return nil, 200, h.management.GrantRoute(ctx, a, id, in.RouteID, status, in.ExpiresAt, QuotaLimits{Daily: in.QuotaDaily, Monthly: in.QuotaMonthly})
	case "/api-clients/:id/routes/:grant_id":
		grant, err := strconv.ParseInt(gid, 10, 64)
		if err != nil || grant <= 0 {
			return nil, 0, Invalid()
		}
		return nil, 204, h.management.RevokeGrant(ctx, a, id, grant)
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
		name := ""
		if in.Name != nil {
			name = *in.Name
		}
		route := Route{ID: id, Name: name, Code: in.Code, Capability: in.Capability, Method: in.Method, Path: in.Path, RouteGroup: in.RouteGroup, Visibility: in.Visibility, Status: status, BillingMode: in.BillingMode, Version: in.Version, PriceCents: in.PriceCents, UnitsPerCall: in.UnitsPerCall, Currency: in.Currency}
		if err := validateCapability(route); err != nil || len(strings.TrimSpace(name)) == 0 || len(name) > 200 || in.Visibility != "public" && in.Visibility != "private" || status != "enabled" && status != "disabled" || id > 0 && in.Version <= 0 {
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

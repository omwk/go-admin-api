package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// ObservationRoutes declares the complete observation API and permission map.
func ObservationRoutes() []AdminRoute {
	return []AdminRoute{
		{"GET", "/api-calls", permission.GatewayCallRead},
		{"GET", "/api-calls/summary", permission.GatewayCallRead},
		{"POST", "/api-clients/:id/try", permission.GatewayClientManage},
	}
}

func (h *AdminHandler) observe(c *gin.Context, route AdminRoute, id int64) {
	if h.observation == nil {
		httpx.Fail(c, Unavailable())
		return
	}
	s := h.observation
	user := identity.AdminActor(c).User
	scope := ObservationScope{Actor: Actor{TenantID: user.TenantID, UserID: user.ID}, CrossTenant: user.IsSuper && user.TenantID == s.platformID}
	c.Header("Cache-Control", "private, no-store")
	if route.Method == "GET" {
		out, err := s.read(c.Request.Context(), scope, c.Request.URL.Query(), route.Path == "/api-calls/summary")
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
	var in TryInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		httpx.Fail(c, Invalid())
		return
	}
	// Gin has already applied the engine's configured trusted proxy policy.
	source := net.JoinHostPort(c.ClientIP(), "0")
	out, err := s.Try(c.Request.Context(), scope, id, in, source)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, out)
}

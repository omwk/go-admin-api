package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Capability declares a code-reviewed public method, path and bounded handler.
type Capability struct{ Code, Method, Path, Group string }

// Capabilities is the entire M8a public business capability manifest.
func Capabilities() []Capability {
	return []Capability{{Code: "order.status", Method: "GET", Path: "/open/v1/orders/:id/status", Group: "orders"}}
}

// Route is primary-state configuration for an explicitly registered capability.
type Route struct {
	ID          int64  `json:"id"`
	TenantID    int64  `json:"-"`
	CreatedBy   int64  `json:"created_by"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Capability  string `json:"capability"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	RouteGroup  string `json:"route_group"`
	Visibility  string `json:"visibility"`
	Status      string `json:"status"`
	BillingMode string `json:"billing_mode"`
	Version     int    `json:"version"`
}

// RouteStore supplies uncached route gates and application grants.
type RouteStore interface {
	Route(context.Context, Principal, string) (Route, error)
	RouteGranted(context.Context, Principal, int64, time.Time) (bool, error)
}

// OpenOrderStatus is an independent public DTO, not an internal order projection.
type OpenOrderStatus struct {
	ID        string    `json:"id"`
	OrderNo   string    `json:"order_no"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OrderStatusReader verifies application resource ceilings before returning public facts.
type OrderStatusReader interface {
	OrderStatus(context.Context, Principal, string) (OpenOrderStatus, error)
}

func registeredRoute(route Route, capability Capability) bool {
	return route.Code == capability.Code && route.Capability == capability.Code &&
		route.Method == capability.Method && route.Path == capability.Path &&
		route.RouteGroup == capability.Group && route.Visibility == "public" &&
		route.Status == "enabled" && route.BillingMode == "free"
}

func matchCapability(r *http.Request) (Capability, string, bool) {
	if r == nil || r.URL == nil || strings.ContainsAny(r.URL.EscapedPath(), "%\\") {
		return Capability{}, "", false
	}
	for _, capability := range Capabilities() {
		if r.Method != capability.Method {
			continue
		}
		template := strings.Split(capability.Path, "/")
		parts := strings.Split(r.URL.Path, "/")
		if len(template) != len(parts) {
			continue
		}
		id, matched := "", true
		for i, part := range template {
			if part == ":id" {
				id = parts[i]
				matched = matched && id != "" && id != "." && id != ".."
			} else {
				matched = matched && part == parts[i]
			}
		}
		if matched {
			return capability, id, true
		}
	}
	return Capability{}, "", false
}

func validateCapability(route Route) error {
	for _, capability := range Capabilities() {
		if route.Code == capability.Code && route.Capability == capability.Code && route.Method == capability.Method && route.Path == capability.Path && route.RouteGroup == capability.Group && route.BillingMode == "free" {
			return nil
		}
	}
	return errors.New("gateway route capability is not registered")
}

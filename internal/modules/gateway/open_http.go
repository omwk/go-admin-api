package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
)

var correlationID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

type openEnvelope struct {
	Code      int            `json:"code"`
	Message   string         `json:"message"`
	Data      any            `json:"data,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	TraceID   string         `json:"trace_id"`
	RequestID string         `json:"request_id,omitempty"`
}

// OpenHandler owns a middleware stack separate from management and share.
type OpenHandler struct {
	auth   *Authenticator
	routes RouteStore
	orders OrderStatusReader
	calls  *CallService
	limits RateLimiter
	now    func() time.Time
}

// NewOpenHandler accepts only explicit public service ports.
func NewOpenHandler(auth *Authenticator, routes RouteStore, orders OrderStatusReader, calls *CallService, limits RateLimiter, now func() time.Time) (*OpenHandler, error) {
	if auth == nil || routes == nil || orders == nil || calls == nil || limits == nil || now == nil {
		return nil, errors.New("gateway open dependencies are required")
	}
	return &OpenHandler{auth: auth, routes: routes, orders: orders, calls: calls, limits: limits, now: now}, nil
}

// Dispatch selects open requests before either application's middleware stack runs.
func Dispatch(open, admin http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open" || strings.HasPrefix(r.URL.Path, "/open/") {
			open.ServeHTTP(w, r)
			return
		}
		admin.ServeHTTP(w, r)
	})
}

func (h *OpenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	traceID, requestID := ulid.Make().String(), r.Header.Get("X-Request-Id")
	if !correlationID.MatchString(requestID) {
		requestID = ""
	}
	r = r.WithContext(logger.WithTraceID(r.Context(), traceID))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Request-Id", traceID)
	w.Header().Set("Referrer-Policy", "no-referrer")
	defer func() {
		if recover() != nil {
			h.write(w, traceID, requestID, nil, errs.New(10002, 500, "服务处理失败"))
		}
	}()
	if r.Method == "GET" && (r.URL.Path == "/open/v1/usage" || r.URL.Path == "/open/v1/balance") {
		h.builtin(w, r, traceID, requestID)
		return
	}
	capability, id, ok := matchCapability(r)
	if !ok {
		h.write(w, traceID, requestID, nil, RouteUnavailable())
		return
	}
	principal, err := h.auth.Authenticate(r.Context(), r)
	if err != nil {
		h.write(w, traceID, requestID, nil, err)
		return
	}
	h.order(w, r, traceID, requestID, principal, capability, id)
}

func (h *OpenHandler) order(w http.ResponseWriter, r *http.Request, traceID, requestID string, principal Principal, capability Capability, id string) {
	call := h.calls.NewCall(principal, Route{}, traceID, requestID, h.auth.source(r))
	route, err := h.routes.Route(r.Context(), principal, capability.Code)
	if err != nil {
		h.write(w, traceID, requestID, nil, err)
		return
	}
	call.RouteID, call.RouteVersion, call.Method, call.Path = route.ID, route.Version, route.Method, route.Path
	w.Header().Set("X-Call-Id", call.ID)
	if err == nil && (!registeredRoute(route, capability) || route.TenantID != principal.TenantID) {
		err = RouteUnavailable()
	}
	if err == nil {
		var allowed bool
		allowed, err = h.routes.RouteGranted(r.Context(), principal, route.ID, h.now().UTC())
		if err == nil && !allowed {
			err = errs.New(80104, 403, "未获授权访问此接口")
		}
	}
	if err != nil {
		h.write(w, traceID, requestID, nil, err)
		return
	}
	if _, err := ulid.ParseStrict(id); err != nil || r.URL.RawQuery != "" {
		h.write(w, traceID, requestID, nil, h.calls.Reject(r.Context(), call, Invalid()))
		return
	}
	decision, err := h.limits.Take(r.Context(), principal, route.ID)
	writeRateHeaders(w, decision)
	if err != nil {
		h.write(w, traceID, requestID, nil, h.calls.Reject(r.Context(), call, err))
		return
	}
	data, err := h.calls.Run(r.Context(), call, func(ctx context.Context) (OpenOrderStatus, error) {
		status, err := h.orders.OrderStatus(ctx, principal, id)
		if err == nil {
			err = h.recheck(r.WithContext(ctx), principal, route, capability)
		}
		return status, err
	})
	h.write(w, traceID, requestID, data, err)
}

func (h *OpenHandler) recheck(r *http.Request, principal Principal, original Route, capability Capability) error {
	if err := h.auth.store.Touch(r.Context(), principal, h.now().UTC()); err != nil {
		return err
	}
	route, err := h.routes.Route(r.Context(), principal, capability.Code)
	if err != nil {
		return err
	}
	if !registeredRoute(route, capability) || route.Version != original.Version {
		return RouteUnavailable()
	}
	allowed, err := h.routes.RouteGranted(r.Context(), principal, route.ID, h.now().UTC())
	if err != nil {
		return err
	}
	if !allowed {
		return errs.New(80104, 403, "未获授权访问此接口")
	}
	return nil
}

func (*OpenHandler) write(w http.ResponseWriter, traceID, requestID string, data any, err error) {
	envelope := openEnvelope{Code: 0, Message: "ok", Data: data, TraceID: traceID, RequestID: requestID}
	status := 200
	if err != nil {
		app := errs.From(PublicError(err))
		envelope.Code, envelope.Message, envelope.Data, status = app.Code, app.Message, nil, app.HTTPStatus
		envelope.Details = app.Details
		var limited *LimitError
		if errors.As(err, &limited) {
			writeRateHeaders(w, limited.Decision)
		}
	}
	body, encodeErr := json.Marshal(envelope)
	if encodeErr != nil {
		http.Error(w, "服务处理失败", 500)
		return
	}
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		return
	}
}

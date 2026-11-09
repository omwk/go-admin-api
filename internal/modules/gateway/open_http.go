package gateway

import (
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
	Code      int              `json:"code"`
	Message   string           `json:"message"`
	Data      *OpenOrderStatus `json:"data,omitempty"`
	TraceID   string           `json:"trace_id"`
	RequestID string           `json:"request_id,omitempty"`
}

// OpenHandler owns a middleware stack separate from management and share.
type OpenHandler struct {
	auth   *Authenticator
	routes RouteStore
	orders OrderStatusReader
	now    func() time.Time
}

// NewOpenHandler accepts only explicit public service ports.
func NewOpenHandler(auth *Authenticator, routes RouteStore, orders OrderStatusReader, now func() time.Time) (*OpenHandler, error) {
	if auth == nil || routes == nil || orders == nil || now == nil {
		return nil, errors.New("gateway open dependencies are required")
	}
	return &OpenHandler{auth: auth, routes: routes, orders: orders, now: now}, nil
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
	route, err := h.routes.Route(r.Context(), principal, capability.Code)
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
	if r.URL.RawQuery != "" {
		h.write(w, traceID, requestID, nil, Invalid())
		return
	}
	status, err := h.orders.OrderStatus(r.Context(), principal, id)
	if err == nil {
		err = h.recheck(r, principal, route, capability)
	}
	h.write(w, traceID, requestID, &status, err)
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

func (*OpenHandler) write(w http.ResponseWriter, traceID, requestID string, data *OpenOrderStatus, err error) {
	envelope := openEnvelope{Code: 0, Message: "ok", Data: data, TraceID: traceID, RequestID: requestID}
	status := 200
	if err != nil {
		app := errs.From(PublicError(err))
		envelope.Code, envelope.Message, envelope.Data, status = app.Code, app.Message, nil, app.HTTPStatus
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

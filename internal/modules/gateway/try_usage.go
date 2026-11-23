package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// trialUsage uses the existing read-only handler with trial admission and metadata.
func (h *OpenHandler) trialUsage(w http.ResponseWriter, r *http.Request, trace, request string, p Principal) {
	route, err := h.usageTrialRoute(r, p)
	if err != nil {
		h.write(w, trace, request, nil, h.rejectTrial(w, r, trace, request, err))
		return
	}
	call := h.calls.NewCall(p, route, trace, request, h.auth.source(r))
	call.ChargeSource = "manual_test"
	w.Header().Set("X-Call-Id", call.ID)
	err = h.calls.bindRequest(&call, r)
	if err == nil {
		var decision RateDecision
		decision, err = h.limits.Take(r.Context(), p, route.ID)
		writeRateHeaders(w, decision)
	}
	if err == nil {
		err = h.calls.Start(r.Context(), call)
	}
	if err != nil {
		h.write(w, trace, request, nil, h.calls.Reject(r.Context(), call, err))
		return
	}
	work, cancel := context.WithTimeout(r.Context(), h.calls.policy.Timeout)
	defer cancel()
	usage, err := h.readUsage(r.WithContext(work), p)
	if err == nil {
		capability, _, _ := matchCapability(r)
		err = h.recheck(r.WithContext(work), p, route, capability)
	}
	result := CallResult{StatusCode: 200, Data: json.RawMessage(`{}`), Attempts: 1}
	if err != nil {
		app := errs.From(PublicError(err))
		result.StatusCode, result.ErrorCode, result.Data = app.HTTPStatus, strconv.Itoa(app.Code), nil
	}
	finish, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer stop()
	if finishErr := h.calls.Finish(finish, call.ID, result); finishErr != nil {
		err = Unavailable()
	}
	h.write(w, trace, request, usage, err)
}

func (h *OpenHandler) usageTrialRoute(r *http.Request, p Principal) (Route, error) {
	trial := r.Context().Value(trialContextKey{}).(trialContext)
	route, err := h.routes.Route(r.Context(), p, "usage.read")
	if err != nil {
		return Route{}, err
	}
	capability, _, ok := matchCapability(r)
	if !ok || !registeredRoute(route, capability) || route.TenantID != p.TenantID ||
		route.ID != trial.route.ID || p.ClientID != trial.client.ID {
		return Route{}, RouteUnavailable()
	}
	return route, h.recheck(r, p, route, capability)
}

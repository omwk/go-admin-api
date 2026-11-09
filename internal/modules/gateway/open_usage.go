package gateway

import (
	"math"
	"net/http"
	"strconv"
)

func (h *OpenHandler) builtin(w http.ResponseWriter, r *http.Request, traceID, requestID string) {
	principal, err := h.auth.Authenticate(r.Context(), r)
	if err != nil {
		h.write(w, traceID, requestID, nil, err)
		return
	}
	decision, err := h.limits.Take(r.Context(), principal, 0)
	writeRateHeaders(w, decision)
	if err != nil {
		h.write(w, traceID, requestID, nil, err)
		return
	}
	if r.URL.Path == "/open/v1/balance" {
		h.write(w, traceID, requestID, nil, Unavailable())
		return
	}
	query := r.URL.Query()
	if len(query) > 1 || len(query) == 1 && (len(query["date"]) != 1 || query.Get("date") == "") {
		h.write(w, traceID, requestID, nil, Invalid())
		return
	}
	usage, err := h.calls.Usage(r.Context(), principal, query.Get("date"))
	if err == nil {
		err = h.auth.store.Touch(r.Context(), principal, h.now().UTC())
	}
	h.write(w, traceID, requestID, usage, err)
}

func writeRateHeaders(w http.ResponseWriter, decision RateDecision) {
	if decision.Limit <= 0 {
		return
	}
	w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(decision.Limit, 10))
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(decision.Remaining, 10))
	if decision.RetryAfter > 0 {
		seconds := max(1, int64(math.Ceil(decision.RetryAfter.Seconds())))
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	}
}

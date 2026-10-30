package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
)

// Timeout buffers responses at the HTTP boundary and cancels overdue requests.
// net/http owns the worker lifetime, so a timed-out Gin context is not reused early.
func Timeout(next http.Handler, duration time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = ulid.Make().String()
		}
		ctx, cancel := context.WithTimeout(logger.WithTraceID(r.Context(), id), duration)
		defer cancel()
		body, err := json.Marshal(errorResponse{
			Code: errs.ErrUpstreamTimeout, Message: "请求超时", TraceID: id, RequestID: r.Header.Get("X-Request-Id"),
		})
		if err != nil {
			http.Error(w, "response encoding failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Request-Id", id)
		http.TimeoutHandler(next, duration+time.Second, string(body)).ServeHTTP(&deadlineWriter{ResponseWriter: w, ctx: ctx, body: body}, r.WithContext(ctx))
	})
}

type deadlineWriter struct {
	http.ResponseWriter
	ctx           context.Context
	body          []byte
	expired, sent bool
}

func (w *deadlineWriter) WriteHeader(status int) {
	if w.ctx.Err() != nil {
		w.expired = true
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Encoding")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		status = http.StatusGatewayTimeout
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *deadlineWriter) Write(body []byte) (int, error) {
	if !w.expired {
		return w.ResponseWriter.Write(body)
	}
	if w.sent {
		return len(body), nil
	}
	w.sent = true
	if _, err := w.ResponseWriter.Write(w.body); err != nil {
		return 0, err
	}
	return len(body), nil
}

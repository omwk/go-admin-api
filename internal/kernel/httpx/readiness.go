package httpx

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
)

type readiness struct {
	db, redis Pinger
	mu        sync.Mutex
	expires   time.Time
	checks    map[string]string
}

func (r *readiness) check(ctx context.Context) map[string]string {
	// Hold the mutex through refresh so concurrent probes share one DB/Redis ping.
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Now().Before(r.expires) {
		return r.checks
	}
	checks := make(map[string]string, 2)
	for name, dependency := range map[string]Pinger{"db": r.db, "redis": r.redis} {
		checks[name] = "ok"
		if dependency == nil || dependency.Ping(ctx) != nil {
			checks[name] = "unavailable"
		}
	}
	r.checks = checks
	r.expires = time.Now().Add(time.Second)
	return checks
}

func (r *readiness) handle(c *gin.Context) {
	checks := r.check(c.Request.Context())
	status, httpStatus, code, message := "ok", http.StatusOK, 0, "ok"
	if checks["db"] != "ok" || checks["redis"] != "ok" {
		status, httpStatus, code, message = "degraded", http.StatusServiceUnavailable, errs.ErrInternal, "依赖不可用"
	}
	c.JSON(httpStatus, struct {
		response
		Status string `json:"status"`
	}{response: response{
		Code: code, Message: message, Data: checks,
		TraceID: logger.TraceID(c.Request.Context()), RequestID: c.GetString("request_id"),
	}, Status: status})
}

package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/stretchr/testify/require"
)

func testSystemAuth() *SystemAuthentication {
	s := &SystemAuthentication{}
	s.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer synthetic-probe" {
			c.AbortWithStatus(403)
			return
		}
		c.Next()
	})
	return s
}

func TestSystemRoutesRejectAnonymousBeforeProbing(t *testing.T) {
	for _, guard := range []*SystemAuthentication{nil, {}, testSystemAuth()} {
		log, err := logger.New(io.Discard, "info", "json")
		require.NoError(t, err)
		db, redis := &probe{}, &probe{}
		r, err := NewEngine(Dependencies{DB: db, Redis: redis, Log: log, SystemAuth: guard})
		require.NoError(t, err)
		for _, path := range []string{"/healthz", "/readyz", "/metrics", "/api/v1/admin/system/info"} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, 403, w.Code, path)
			require.NotContains(t, w.Body.String(), "go_version")
		}
		require.Zero(t, db.calls.Load())
		require.Zero(t, redis.calls.Load())
	}
}

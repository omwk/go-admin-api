package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/stretchr/testify/require"
)

func TestResponseStatusAndEnvelope(t *testing.T) {
	// REQ-SYS-001: response envelopes and status codes match the public contract.
	for _, tc := range []struct {
		name    string
		handler gin.HandlerFunc
		status  int
		code    int
	}{
		{"ok", func(c *gin.Context) { OK(c, gin.H{"id": 1}) }, 200, 0},
		{"created", func(c *gin.Context) { Created(c, gin.H{"id": 1}) }, 201, 0},
		{"no content", NoContent, 204, 0},
		{"invalid", func(c *gin.Context) { Fail(c, errs.Invalid(errors.New("bad input"))) }, 400, 10001},
		{"internal", func(c *gin.Context) { Fail(c, errors.New("database failed")) }, 500, 10002},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(TraceID())
			router.GET("/test", tc.handler)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test", nil))
			require.Equal(t, tc.status, w.Code)
			if tc.status == http.StatusNoContent {
				require.Empty(t, w.Body.String())
				return
			}
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.EqualValues(t, tc.code, body["code"])
			require.NotEmpty(t, body["message"])
			require.Equal(t, w.Header().Get("X-Request-Id"), body["trace_id"])
			require.NotContains(t, w.Body.String(), "database failed")
			if tc.code == 0 {
				require.Equal(t, map[string]any{"id": float64(1)}, body["data"])
			}
		})
	}
}

func TestTraceIDPropagatesToResponseAndLogs(t *testing.T) {
	// REQ-SYS-003: a request keeps its trace identifier across all log records.
	for _, requestID := range []string{"", "client-request"} {
		t.Run(requestID, func(t *testing.T) {
			var output bytes.Buffer
			log, err := logger.New(&output, "debug", "json")
			require.NoError(t, err)
			router := gin.New()
			router.Use(TraceID())
			router.GET("/test", func(c *gin.Context) {
				ctx := logger.With(c.Request.Context(), log)
				for i := 0; i < 3; i++ {
					logger.From(ctx).InfoContext(ctx, "request", "module", "test", "action", "read")
				}
				OK(c, nil)
			})
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("X-Request-Id", requestID)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			traceID := w.Header().Get("X-Request-Id")
			if requestID == "" {
				_, err := ulid.ParseStrict(traceID)
				require.NoError(t, err)
			} else {
				require.Equal(t, requestID, traceID)
				require.Contains(t, w.Body.String(), `"request_id":"client-request"`)
			}
			lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
			require.Len(t, lines, 3)
			for _, line := range lines {
				var record map[string]any
				require.NoError(t, json.Unmarshal(line, &record))
				require.Equal(t, traceID, record["trace_id"])
			}
		})
	}
}

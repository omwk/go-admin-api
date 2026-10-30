package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/stretchr/testify/require"
)

func TestEnginePropagatesTraceAcrossThreeLogs(t *testing.T) {
	// REQ-SYS-003: handler, dependency and access logs share the response trace.
	var logs bytes.Buffer
	log, err := logger.New(&logs, "info", "json")
	require.NoError(t, err)
	r, err := NewEngine(Dependencies{DB: &probe{}, Redis: &probe{}, Log: log})
	require.NoError(t, err)
	r.GET("/trace", func(c *gin.Context) {
		ctx := c.Request.Context()
		logger.From(ctx).InfoContext(ctx, "handler reached", "module", "test", "action", "handler")
		logTraceDependency(ctx)
		OK(c, nil)
	})
	req := httptest.NewRequest("GET", "/trace", nil)
	req.Header.Set("X-Request-Id", "acceptance-trace")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "acceptance-trace", w.Header().Get("X-Request-Id"))
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "acceptance-trace", body["trace_id"])
	decoder := json.NewDecoder(&logs)
	var actions []string
	for decoder.More() {
		var record map[string]any
		require.NoError(t, decoder.Decode(&record))
		require.Equal(t, body["trace_id"], record["trace_id"])
		actions = append(actions, record["action"].(string))
	}
	require.Equal(t, []string{"handler", "dependency", "request"}, actions)
}

func logTraceDependency(ctx context.Context) {
	logger.From(ctx).InfoContext(ctx, "dependency reached", "module", "test", "action", "dependency")
}

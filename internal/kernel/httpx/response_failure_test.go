package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/stretchr/testify/require"
)

func TestFailUnknownErrorEnvelopeAndLogs(t *testing.T) {
	// BE-4 §2 (P0): private failures must produce one safe, traceable envelope.
	cause := errors.New(`SELECT id FROM product_spu: duplicate key violates constraint "product_spu_pkey"`)
	wrapped := errs.New(errs.ErrInternal, 500, "服务内部错误")
	wrapped.Cause = cause
	for _, failure := range []error{cause, fmt.Errorf("create product: %w", cause), wrapped} {
		t.Run(fmt.Sprintf("%T", failure), func(t *testing.T) {
			var logs bytes.Buffer
			log, err := logger.New(&logs, "debug", "json")
			require.NoError(t, err)
			router, err := NewEngine(Dependencies{DB: &probe{}, Redis: &probe{}, Log: log})
			require.NoError(t, err)
			router.POST("/failure", func(c *gin.Context) { Fail(c, failure) })
			req := httptest.NewRequest(http.MethodPost, "/failure", nil)
			req.Header.Set("X-Request-Id", "be4-failure-trace")
			w := httptest.NewRecorder()
			Timeout(router, time.Second).ServeHTTP(w, req)
			require.Equal(t, 500, w.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.EqualValues(t, 10002, body["code"])
			require.Equal(t, "服务内部错误", body["message"])
			require.Contains(t, body, "data")
			require.Nil(t, body["data"])
			require.Equal(t, "be4-failure-trace", body["trace_id"])
			require.Equal(t, body["trace_id"], body["request_id"])
			for _, private := range []string{"SELECT", "product_spu", "constraint", "create product"} {
				require.NotContains(t, w.Body.String(), private)
			}
			found := false
			decoder := json.NewDecoder(&logs)
			for decoder.More() {
				var record map[string]any
				require.NoError(t, decoder.Decode(&record))
				require.Equal(t, body["trace_id"], record["trace_id"])
				require.EqualValues(t, 10002, record["error_code"])
				if record["error"] != nil {
					require.Contains(t, record["error"], cause.Error())
					found = true
				}
			}
			require.True(t, found, "original error must be present in server logs")
			t.Logf("status=%d body=%s", w.Code, w.Body.String())
		})
	}
}

func TestFailPreservesKnownMappingsAndAborts(t *testing.T) {
	for _, tc := range []struct{ status, code int }{{400, 10001}, {403, 20107}, {404, 40101}, {409, 10409}, {500, 10002}, {500, 50401}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			router := gin.New()
			router.Use(TraceID())
			called := false
			app := errs.New(tc.code, tc.status, "public message")
			app.Details = map[string]any{"id": 7}
			router.GET("/failure", func(c *gin.Context) { Fail(c, fmt.Errorf("private wrapper: %w", app)) },
				func(*gin.Context) { called = true })
			w := request(router, "GET", "/failure", nil)
			require.Equal(t, tc.status, w.Code)
			require.False(t, called)
			decoder := json.NewDecoder(w.Body)
			var body map[string]any
			require.NoError(t, decoder.Decode(&body))
			require.EqualValues(t, tc.code, body["code"])
			require.Equal(t, app.Message, body["message"])
			require.Equal(t, map[string]any{"id": float64(7)}, body["details"])
			require.NotContains(t, body, "data", "mapped errors retain their existing envelope")
			require.NotEmpty(t, body["trace_id"])
			require.ErrorIs(t, decoder.Decode(&body), io.EOF)
		})
	}
}

func TestFailFallsBackBeforeWritingUnencodableDetails(t *testing.T) {
	app := errs.New(errs.ErrInvalidParam, 400, "public message")
	app.Details = map[string]any{"invalid": json.RawMessage(`{`)}
	router, _, _ := testEngine(t)
	router.GET("/failure", func(c *gin.Context) { Fail(c, app) })
	w := request(router, "GET", "/failure", nil)
	require.Equal(t, 500, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.EqualValues(t, 10002, body["code"])
	require.NotEmpty(t, body["trace_id"])
	require.NotContains(t, body, "details")
	require.Contains(t, body, "data")
	require.Nil(t, body["data"])
}

func TestFailDoesNotAppendToWrittenResponse(t *testing.T) {
	router, _, _ := testEngine(t)
	router.GET("/failure", func(c *gin.Context) {
		Fail(c, errors.New("first failure"))
		Fail(c, errors.New("second failure"))
	})
	w := request(router, "GET", "/failure", nil)
	decoder := json.NewDecoder(w.Body)
	var body errorResponse
	require.NoError(t, decoder.Decode(&body))
	require.Equal(t, 10002, body.Code)
	require.ErrorIs(t, decoder.Decode(&body), io.EOF)
}

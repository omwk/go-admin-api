package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/stretchr/testify/require"
)

type probe struct {
	calls  atomic.Int32
	failed atomic.Bool
}

func (p *probe) Ping(context.Context) error {
	p.calls.Add(1)
	if p.failed.Load() {
		return errors.New("dependency failure")
	}
	return nil
}

func testEngine(t *testing.T) (*gin.Engine, *probe, *probe) {
	t.Helper()
	log, err := logger.New(io.Discard, "info", "json")
	require.NoError(t, err)
	db, redis := &probe{}, &probe{}
	r, err := NewEngine(Dependencies{DB: db, Redis: redis, Log: log, Build: BuildInfo{Version: "test"}, SystemAuth: testSystemAuth()})
	require.NoError(t, err)
	return r, db, redis
}

func request(r http.Handler, method, path string, body io.Reader) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Authorization", "Bearer synthetic-probe")
	r.ServeHTTP(w, req)
	return w
}

func TestReadinessCachesAndCoalescesProbes(t *testing.T) {
	// REQ-SYS-005: probes share a one-second result, including failures.
	r, db, redis := testEngine(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request(r, "GET", "/readyz", nil)
			if w.Code != 200 {
				t.Error(w.Code)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, db.calls.Load())
	require.EqualValues(t, 1, redis.calls.Load())
	redis.failed.Store(true)
	require.Equal(t, 200, request(r, "GET", "/readyz", nil).Code)
	require.Eventually(t, func() bool { return request(r, "GET", "/readyz", nil).Code == 503 }, 2*time.Second, 50*time.Millisecond)
	w := request(r, "GET", "/readyz", nil)
	require.Contains(t, w.Body.String(), `"status":"degraded"`)
	require.Contains(t, w.Body.String(), `"redis":"unavailable"`)
	require.NotContains(t, w.Body.String(), "dependency failure")
	require.Equal(t, 200, request(r, "GET", "/healthz", nil).Code)
}

func TestEngineRecoveryErrorsCORSAndBodyLimit(t *testing.T) {
	r, _, _ := testEngine(t)
	r.GET("/panic", func(*gin.Context) { panic("private panic value") })
	r.POST("/body", func(c *gin.Context) { OK(c, nil) })
	for _, tc := range []struct {
		path, method string
		body         io.Reader
		status       int
	}{
		{"/panic", "GET", nil, 500}, {"/missing", "GET", nil, 404},
		{"/body", "POST", strings.NewReader(strings.Repeat("x", (1<<20)+1)), 413},
		{"/body", "POST", strings.NewReader("{}"), 200}, {"/body", "OPTIONS", nil, 204},
	} {
		w := request(r, tc.method, tc.path, tc.body)
		require.Equal(t, tc.status, w.Code)
		require.NotEmpty(t, w.Header().Get("X-Request-Id"))
		require.NotContains(t, w.Body.String(), "private panic value")
		if tc.status >= 400 {
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.NotZero(t, body["code"])
			require.NotEmpty(t, body["message"])
			require.Equal(t, w.Header().Get("X-Request-Id"), body["trace_id"])
		}
	}
	req := httptest.NewRequest("OPTIONS", "/body", nil)
	req.Header.Set("Origin", "https://client.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestBodyLimitBypassesStreamingFileRoute(t *testing.T) {
	r, _, _ := testEngine(t)
	var received int64
	r.POST("/api/v1/admin/files", func(c *gin.Context) {
		var err error
		received, err = io.Copy(io.Discard, c.Request.Body)
		require.NoError(t, err)
		OK(c, nil)
	})
	body := strings.Repeat("x", (1<<20)+1)
	w := request(r, http.MethodPost, "/api/v1/admin/files", strings.NewReader(body))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(len(body)), received)
}

func TestBodyLimitWithoutBypassStillRejectsOversize(t *testing.T) {
	r := gin.New()
	r.Use(BodyLimit(8))
	called := false
	r.POST("/body", func(c *gin.Context) { called = true; OK(c, nil) })
	w := request(r, http.MethodPost, "/body", strings.NewReader("123456789"))
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	require.False(t, called)
}

func TestTimeoutBoundsResponseAndCancelsHandler(t *testing.T) {
	r, _, _ := testEngine(t)
	finished := make(chan struct{})
	r.GET("/slow", func(c *gin.Context) { <-c.Request.Context().Done(); close(finished) })
	w := request(Timeout(r, 20*time.Millisecond), "GET", "/slow", nil)
	require.Equal(t, 504, w.Code)
	require.Contains(t, w.Body.String(), `"code":10007`)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, w.Header().Get("X-Request-Id"), body["trace_id"])
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("handler did not exit")
	}
}

func TestSystemInfoAndMetrics(t *testing.T) {
	r, _, _ := testEngine(t)
	require.Contains(t, request(r, "GET", "/api/v1/admin/system/info", nil).Body.String(), `"version":"test"`)
	request(r, "GET", "/healthz", nil)
	w := request(r, "GET", "/metrics", nil)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "http_requests_total")
	require.Contains(t, w.Body.String(), "http_request_duration_seconds")
}

func TestAccessLogOmitsQueryHeadersAndBody(t *testing.T) {
	var logs bytes.Buffer
	log, err := logger.New(&logs, "info", "json")
	require.NoError(t, err)
	r, err := NewEngine(Dependencies{DB: &probe{}, Redis: &probe{}, Log: log, SystemAuth: testSystemAuth()})
	require.NoError(t, err)
	w := request(r, "GET", "/healthz?private=do-not-log-this", nil)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, logs.String(), "do-not-log-this")
	require.Contains(t, logs.String(), w.Header().Get("X-Request-Id"))
}

func TestBodyLimitRejectsChunkedRequest(t *testing.T) {
	r, _, _ := testEngine(t)
	called := false
	r.POST("/body", func(c *gin.Context) { called = true; OK(c, nil) })
	req := httptest.NewRequest("POST", "/body", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 413, w.Code)
	require.False(t, called)
}

func TestTimeoutDiscardsBufferedBodyAndLength(t *testing.T) {
	r, _, _ := testEngine(t)
	finished, release := make(chan struct{}), make(chan struct{})
	r.GET("/blocked", func(c *gin.Context) {
		defer close(finished)
		c.Header("Content-Length", "1")
		c.String(200, "x")
		<-release
	})
	w := request(Timeout(r, 20*time.Millisecond), "GET", "/blocked", nil)
	close(release)
	<-finished
	require.Equal(t, 504, w.Code)
	require.Empty(t, w.Header().Get("Content-Length"))
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.EqualValues(t, 10007, body["code"])
}

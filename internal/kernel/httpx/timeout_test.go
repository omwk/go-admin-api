package httpx

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTimeoutReplacesCanceledHandlerResponse(t *testing.T) {
	r, _, _ := testEngine(t)
	r.GET("/canceled", func(c *gin.Context) {
		<-c.Request.Context().Done()
		c.Header("Content-Length", "1")
		c.Header("Content-Encoding", "gzip")
		c.String(200, "x")
	})
	req := httptest.NewRequest("GET", "/canceled", nil)
	req.Header.Set("X-Request-Id", "client-request")
	w := httptest.NewRecorder()
	Timeout(r, 20*time.Millisecond).ServeHTTP(w, req)
	require.Equal(t, 504, w.Code)
	require.Empty(t, w.Header().Get("Content-Length"))
	require.Empty(t, w.Header().Get("Content-Encoding"))
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "client-request", body["trace_id"])
	require.Equal(t, "client-request", body["request_id"])
}

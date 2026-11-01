package httpx

import (
	"bytes"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestExtractIdempotencyRestoresBodyAndRouteMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/shops/:id/credentials", func(c *gin.Context) {
		input, enabled, err := ExtractIdempotency(c)
		require.NoError(t, err)
		require.True(t, enabled)
		require.Equal(t, "POST", input.Method)
		require.Equal(t, "/shops/:id/credentials", input.Route)
		require.Equal(t, []string{"42"}, input.PathParams)
		require.Equal(t, []byte(`{"x":1}`), input.Body)
		restored, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, input.Body, restored)
		c.Status(204)
	})
	req := httptest.NewRequest("POST", "/shops/42/credentials", bytes.NewReader([]byte(`{"x":1}`)))
	req.Header.Set("Idempotency-Key", "raw-client-key")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	require.Equal(t, 204, resp.Code)
}

package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestOpenAPIRouteUsesDeploymentOrigin(t *testing.T) {
	// BE-IDN-032 §4: deployments substitute the server without modifying the embedded contract.
	var original map[string]any
	require.NoError(t, yaml.Unmarshal(OpenAPI, &original))
	for _, origin := range []string{"https://api.example:8443", "http://127.0.0.1:18080"} {
		router := gin.New()
		require.NoError(t, RegisterOpenAPIRoute(router, origin))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi/gateway.yaml", nil))
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "application/yaml; charset=utf-8", w.Header().Get("Content-Type"))

		var rendered map[string]any
		require.NoError(t, yaml.Unmarshal(w.Body.Bytes(), &rendered))
		require.Equal(t, []any{map[string]any{"url": origin}}, rendered["servers"])
		rendered["servers"] = original["servers"]
		require.Equal(t, original, rendered)
	}
	require.Equal(t, []any{map[string]any{"url": "/"}}, original["servers"])
}

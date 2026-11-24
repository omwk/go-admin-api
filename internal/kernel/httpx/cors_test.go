package httpx

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/stretchr/testify/require"
)

func TestEngineCORSOrigins(t *testing.T) {
	// BE-IDN-032 §4: allow an exact origin and keep cache variants distinct.
	const allowed = "https://client.example"
	for _, tc := range []struct {
		name, method, origin, allowOrigin string
		origins                           []string
		status                            int
		vary                              []string
	}{
		{"allowed", "GET", allowed, allowed, []string{allowed}, 200, []string{"Origin", "Accept-Encoding"}},
		{"preflight", "OPTIONS", allowed, allowed, []string{allowed}, 204, []string{"Origin"}},
		{"unlisted", "GET", "https://other.example", "", []string{allowed}, 200, []string{"Origin", "Accept-Encoding"}},
		{"different port", "GET", allowed + ":8080", "", []string{allowed}, 200, []string{"Origin", "Accept-Encoding"}},
		{"no request origin", "GET", "", "", []string{allowed}, 200, []string{"Origin", "Accept-Encoding"}},
		{"disabled", "GET", allowed, "", nil, 200, []string{"Accept-Encoding"}},
		{"disabled preflight", "OPTIONS", allowed, "", nil, 204, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log, err := logger.New(io.Discard, "info", "json")
			require.NoError(t, err)
			r, err := NewEngine(Dependencies{DB: &probe{}, Redis: &probe{}, Log: log, AllowedOrigins: tc.origins})
			require.NoError(t, err)
			r.GET("/resource", func(c *gin.Context) {
				c.Writer.Header().Add("Vary", "Accept-Encoding")
				OK(c, nil)
			})
			req := httptest.NewRequest(tc.method, "/resource", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code)
			require.Equal(t, tc.vary, w.Header().Values("Vary"))
			require.Equal(t, tc.allowOrigin, w.Header().Get("Access-Control-Allow-Origin"))
			for _, name := range []string{"Access-Control-Allow-Methods", "Access-Control-Allow-Headers", "Access-Control-Expose-Headers"} {
				if tc.allowOrigin == "" {
					require.NotContains(t, w.Header(), name)
				} else {
					require.NotEmpty(t, w.Header().Get(name))
				}
			}
			require.NotContains(t, w.Header(), "Access-Control-Allow-Credentials")
		})
	}
}

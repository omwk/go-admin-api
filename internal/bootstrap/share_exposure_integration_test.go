package bootstrap

import (
	"net/http/httptest"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func TestShareExposureIntegrationSystemRoutesRequireLiveSession(t *testing.T) {
	f := newShareAccessFixture(t)
	guard := &httpx.SystemAuthentication{}
	guard.Use(identity.JWTAuth(f.identityFixture.service))
	log, err := logger.New(f.logs, "info", "json")
	require.NoError(t, err)
	router, err := httpx.NewEngine(httpx.Dependencies{DB: f.database, Redis: f.redis, Log: log, SystemAuth: guard})
	require.NoError(t, err)
	paths := []string{"/healthz", "/readyz", "/metrics", "/api/v1/admin/system/info"}
	for _, path := range paths {
		for _, signed := range []bool{false, true} {
			r := httptest.NewRequest("GET", path, nil)
			if signed {
				r.Header.Set("Authorization", "Bearer "+f.bearer)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if signed {
				require.Equal(t, 200, w.Code, path)
			} else {
				require.Equal(t, 401, w.Code, path)
			}
		}
	}
	logout, _ := authRequest(t, f.router, "/api/v1/auth/logout", nil, f.bearer)
	require.Equal(t, 204, logout.Code)
	for _, path := range paths {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+f.bearer)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, 401, w.Code, path)
	}
	t.Log("application: four system routes anonymous=401, live JWT=200, revoked JWT=401")
}

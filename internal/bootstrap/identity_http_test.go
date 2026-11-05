package bootstrap

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

type authEnvelope struct {
	Code int             `json:"code"`
	Data identity.Tokens `json:"data"`
}

func identityRouter(t *testing.T, f *identityFixture) *gin.Engine {
	t.Helper()
	log, err := logger.New(io.Discard, "info", "json")
	require.NoError(t, err)
	router, err := httpx.NewEngine(httpx.Dependencies{DB: f.database, Redis: f.redis, Log: log})
	require.NoError(t, err)
	identity.RegisterAuthRoutes(router, f.service, 1)
	return router
}

func authRequest(t *testing.T, router http.Handler, path string, payload any, bearer string) (*httptest.ResponseRecorder, authEnvelope) {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var envelope authEnvelope
	if response.Code != http.StatusNoContent {
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	}
	return response, envelope
}

func TestIdentityHTTPIntegrationLoginRefreshLogout(t *testing.T) {
	// REQ-IAM-001/002: the registered HTTP flow exposes only the three T1.3 endpoints.
	f := newIdentityFixture(t)
	router := identityRouter(t, f)
	response, login := authRequest(t, router, "/api/v1/auth/login", map[string]string{"username": f.user.Username, "password": f.password}, "")
	require.Equal(t, 200, response.Code)
	require.Zero(t, login.Code)
	require.NotNil(t, login.Data.User)
	require.Equal(t, f.user.ID, login.Data.User.ID)
	response, rotated := authRequest(t, router, "/api/v1/auth/refresh", map[string]string{"refresh_token": login.Data.RefreshToken}, "")
	require.Equal(t, 200, response.Code)
	response, concurrent := authRequest(t, router, "/api/v1/auth/refresh", map[string]string{"refresh_token": login.Data.RefreshToken}, "")
	require.Equal(t, 409, response.Code)
	require.Equal(t, 20109, concurrent.Code)
	response, _ = authRequest(t, router, "/api/v1/auth/logout", nil, rotated.Data.AccessToken)
	require.Equal(t, 204, response.Code)
	response, revoked := authRequest(t, router, "/api/v1/auth/refresh", map[string]string{"refresh_token": rotated.Data.RefreshToken}, "")
	require.Equal(t, 401, response.Code)
	require.Equal(t, 20103, revoked.Code)
	f.server.FlushAll()
	response, revoked = authRequest(t, router, "/api/v1/auth/logout", nil, login.Data.AccessToken)
	require.Equal(t, 401, response.Code)
	require.Equal(t, 20103, revoked.Code)
	for _, route := range router.Routes() {
		require.NotEqual(t, "/api/v1/auth/me", route.Path)
		require.NotEqual(t, "/api/v1/auth/menus", route.Path)
	}
}

func TestIdentityHTTPIntegrationFailuresAndReadiness(t *testing.T) {
	f := newIdentityFixture(t)
	router := identityRouter(t, f)
	response, invalid := authRequest(t, router, "/api/v1/auth/login", map[string]string{}, "")
	require.Equal(t, 400, response.Code)
	require.Equal(t, 10001, invalid.Code)
	response, missing := authRequest(t, router, "/api/v1/auth/logout", nil, "")
	require.Equal(t, 401, response.Code)
	require.Equal(t, 20103, missing.Code)
	issued := f.login(t)
	f.writes.fail = true
	response, failed := authRequest(t, router, "/api/v1/auth/refresh", map[string]string{"refresh_token": issued.RefreshToken}, "")
	require.Equal(t, 503, response.Code)
	require.Equal(t, 10002, failed.Code)
	f.writes.fail = false
	f.server.SetError("ERR dependency unavailable")
	response, failed = authRequest(t, router, "/api/v1/auth/logout", nil, issued.AccessToken)
	require.Equal(t, 503, response.Code)
	require.Equal(t, 10002, failed.Code)
	ready := httptest.NewRecorder()
	router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, 503, ready.Code)
	f.server.SetError("")
	f.now = f.now.Add(15 * time.Minute)
	response, expired := authRequest(t, router, "/api/v1/auth/logout", nil, issued.AccessToken)
	require.Equal(t, 401, response.Code)
	require.Equal(t, 20102, expired.Code)
}

func TestIdentityBootstrapIntegrationRegistersUsableRoutes(t *testing.T) {
	f := newIdentityFixture(t)
	log, err := logger.New(io.Discard, "info", "json")
	require.NoError(t, err)
	router, err := httpx.NewEngine(httpx.Dependencies{DB: f.database, Redis: f.redis, Log: log})
	require.NoError(t, err)
	secret, err := refresh.Generate()
	require.NoError(t, err)
	cfg := &config.Config{JWT: config.JWT{Secret: secret}, Redis: config.Redis{Addr: f.server.Addr(), PoolSize: 4, MinIdleConns: 1}, Storage: config.Storage{Endpoint: "http://127.0.0.1:1", Bucket: "export-tests"}}
	closeIdentity, err := RegisterIdentity(f.ctx, router, f.database, f.redis.cache, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, closeIdentity()) })
	response, login := authRequest(t, router, "/api/v1/auth/login", map[string]string{"username": f.user.Username, "password": f.password}, "")
	require.Equal(t, 200, response.Code)
	response, _ = authRequest(t, router, "/api/v1/auth/logout", nil, login.Data.AccessToken)
	require.Equal(t, 204, response.Code)
}

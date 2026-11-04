package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestSyncIntegrationHTTPIdempotencyAndUntrustedBody(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	path := "/api/v1/admin/sync-jobs/" + itoa(f.jobID) + "/run"
	for _, body := range []any{map[string]any{"tenant_id": 2}, map[string]any{"shop_id": 2}, map[string]any{"scope": map[string]any{}}, map[string]any{"triggered_by": 2}} {
		response, envelope := shopHTTPRequest(t, f.router, http.MethodPost, path, body, f.bearer)
		require.Equal(t, 400, response.Code)
		require.Equal(t, 10001, envelope.Code)
	}
	rawCount(f.ctx, t, f.database, "sync_run", 0)
	var first repo.Run
	for i := 0; i < 2; i++ {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+f.bearer)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "sync-http-repeat")
		response := httptest.NewRecorder()
		f.router.ServeHTTP(response, request)
		require.Equal(t, 201, response.Code, response.Body.String())
		var envelope shopHTTPEnvelope
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		var run repo.Run
		require.NoError(t, json.Unmarshal(envelope.Data, &run))
		if i == 0 {
			first = run
		} else {
			require.Equal(t, first, run)
		}
	}
	rawCount(f.ctx, t, f.database, "sync_run", 1)
	rawCount(f.ctx, t, f.database, "outbox_event", 1)
}

func TestSyncIntegrationMetricsEndpoint(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	f.execute(t, f.run(t))
	log, err := logger.New(f.logs, "info", "json")
	require.NoError(t, err)
	router, err := httpx.NewEngine(httpx.Dependencies{DB: f.database, Redis: f.redis, Log: log,
		Collectors: []prometheus.Collector{NewSyncMetrics(f.ctx, f.database)}})
	require.NoError(t, err)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, 200, response.Code)
	for _, metric := range []string{`sync_run_results{result="success"} 1`, `sync_item_results{result="created"} 1`, `sync_cursor_lag_seconds{resource="product"}`, `sync_drift_open 0`} {
		require.Contains(t, response.Body.String(), metric)
	}
	f.noLeaks(t, "metrics", response.Body.String())
}

func TestSyncOpenAPIRoutesAndPermissionsMatchManifest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi", "sync.yaml"))
	require.NoError(t, err)
	var doc contractMap
	require.NoError(t, yaml.Unmarshal(data, &doc))
	manifest, operations := map[string]string{}, map[string]string{}
	for _, route := range append(syncmodule.Routes(), syncmodule.ReplayRoutes()...) {
		manifest[route.Method+" /api/v1/admin"+routePath(route.Path)] = route.Permission
	}
	for path, raw := range doc["paths"].(contractMap) {
		for method, value := range raw.(contractMap) {
			if method == "parameters" {
				continue
			}
			operation := value.(contractMap)
			code := operation["x-permission"].(string)
			require.NoError(t, permission.Registered(code))
			operations[strings.ToUpper(method)+" "+path] = code
		}
	}
	require.Equal(t, manifest, operations)
}

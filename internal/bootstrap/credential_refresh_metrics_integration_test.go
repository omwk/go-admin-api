package bootstrap

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// REQ-SHP-008 / REQ-SYS-007: API metrics observe persisted worker results.
func TestCredentialRefreshIntegrationMetricsEndpoint(t *testing.T) {
	f, r := wiredCredentialFixture(t)
	run, err := r.prepare(f.ctx, 1, f.target.CredentialID, "manual", &f.actor.UserID)
	require.NoError(t, err)
	require.NoError(t, r.handle(f.ctx, run.ID))
	log, err := logger.New(f.logs, "info", "json")
	require.NoError(t, err)
	system := &httpx.SystemAuthentication{}
	system.Use(identity.JWTAuth(f.identityFixture.service))
	router, err := httpx.NewEngine(httpx.Dependencies{DB: f.database, Redis: f.redis, Log: log, SystemAuth: system, Collectors: []prometheus.Collector{NewCredentialRefreshMetrics(f.ctx, f.database, config.Refresh{})}})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+f.bearer)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, 200, response.Code, response.Body.String())
	for _, metric := range []string{"credential_refresh_success_ratio 1", "credential_refresh_logical_total 1", "credential_refresh_attempts_total 1", "credential_refresh_consecutive_failures 0", "credential_refresh_reconciliation_pending 0", "credential_refresh_refreshing_age_seconds 0"} {
		require.Contains(t, response.Body.String(), metric)
	}
	require.NotContains(t, response.Body.String(), "credential_id=")
}

func TestCredentialRefreshIntegrationMetricsConfiguredLeadTime(t *testing.T) {
	f, _ := wiredCredentialFixture(t)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop_credential SET expires_at=? WHERE id=?", f.now.Add(30*time.Minute), f.target.CredentialID))
	for _, lead := range []time.Duration{5 * time.Minute, time.Hour} {
		collector := NewCredentialRefreshMetrics(f.ctx, f.database, config.Refresh{LeadTime: lead}).(*credentialRefreshMetrics)
		collector.now = func() time.Time { return f.now }
		values, err := collector.snapshot(f.ctx)
		require.NoError(t, err)
		want := float64(0)
		if lead == time.Hour {
			want = 1
		}
		require.Equal(t, want, values[7])
	}
}

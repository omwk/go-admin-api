package bootstrap

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestExportIntegrationMetricsEndpointAndPersistentFailures(t *testing.T) {
	f := newExportFixture(t, 0)
	job := f.create(t, catalog.ExportRequest{})
	first := f.begin(t, job.ID)
	require.NoError(t, f.store.FailExport(f.ctx, first))
	require.NoError(t, f.store.FailExport(f.ctx, first))
	require.NoError(t, f.exports.Execute(f.ctx, 1, job.ID))
	running := f.create(t, catalog.ExportRequest{})
	f.begin(t, running.ID)
	// Reconstruct the collector: completed failures and durations survive retry/restart.
	metrics := NewExportMetrics(f.ctx, f.database)
	var logs bytes.Buffer
	system := &httpx.SystemAuthentication{}
	system.Use(identity.JWTAuth(f.identityFixture.service))
	router, err := httpx.NewEngine(httpx.Dependencies{DB: f.database, Redis: f.redis.cache,
		SystemAuth: system,
		Log:        slog.New(slog.NewJSONHandler(&logs, nil)), Collectors: []prometheus.Collector{metrics}})
	require.NoError(t, err)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+f.bearer)
	router.ServeHTTP(response, request)
	require.Equal(t, 200, response.Code, response.Body.String())
	for _, metric := range []string{`export_running{tenant="1"} 1`, "export_duration_seconds_count 2",
		"export_duration_seconds_bucket{le=\"180\"} 2", "export_failed_total 1", "export_cleanup_pending 2"} {
		require.Contains(t, response.Body.String(), metric)
	}
	require.NotContains(t, response.Body.String(), "attempt-")
	require.NotContains(t, response.Body.String(), "requested_by")
}

func TestExportIntegrationObservationFakeClock(t *testing.T) {
	// ADR-018 §6: exact warning/critical boundaries with a fake observation clock.
	f := newExportFixture(t, 0)
	job := f.create(t, catalog.ExportRequest{})
	f.begin(t, job.ID)
	base := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE export_job SET started_at=? WHERE id=?", base, job.ID))
	key := catalog.ExportObjectKey(catalog.ExportJob{TenantID: 1, ID: job.ID, Attempt: 2})
	require.NoError(t, f.store.QueueExportCleanup(f.ctx, 1, key))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE outbox_event SET created_at=? WHERE event_type='export.cleanup'", base))
	metrics := NewExportMetrics(f.ctx, f.database).(*exportMetrics)
	for _, test := range []struct {
		seconds          int
		running, cleanup string
	}{
		{179, "ok", "ok"}, {180, "warning", "ok"},
		{299, "warning", "ok"}, {300, "critical", "warning"},
		{899, "critical", "warning"}, {900, "critical", "critical"},
	} {
		metrics.now = func() time.Time { return base.Add(time.Duration(test.seconds) * time.Second) }
		snapshot, err := metrics.snapshot(f.ctx)
		require.NoError(t, err)
		require.Equal(t, float64(test.seconds), snapshot.RunningAge)
		require.Equal(t, float64(test.seconds), snapshot.CleanupAge)
		levels := map[string]string{}
		var logs bytes.Buffer
		log := slog.New(slog.NewJSONHandler(&logs, nil))
		observeExport(f.ctx, log, snapshot, levels)
		require.Equal(t, test.running, levels["export_duration"])
		require.Equal(t, test.cleanup, levels["export_cleanup_age"])
		length := logs.Len()
		observeExport(f.ctx, log, snapshot, levels)
		require.Equal(t, length, logs.Len(), "unchanged alerts must not repeat")
		if test.seconds >= 300 {
			require.Contains(t, logs.String(), `"level":"ERROR"`)
		}
	}
}

func TestExportObservationConsecutiveDeleteFailures(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	levels := map[string]string{}
	for _, test := range []struct {
		attempts float64
		level    string
	}{{2, "ok"}, {3, "warning"}, {10, "critical"}, {0, "ok"}, {3, "warning"}} {
		observeExport(context.Background(), log, exportMetricSnapshot{CleanupAttempts: test.attempts}, levels)
		require.Equal(t, test.level, levels["export_cleanup_retries"])
	}
	require.Equal(t, 3, bytes.Count(logs.Bytes(), []byte("export threshold exceeded")))
}

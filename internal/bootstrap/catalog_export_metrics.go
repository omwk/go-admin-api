package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/prometheus/client_golang/prometheus"
)

type exportMetricSnapshot struct {
	Running []struct {
		Tenant int64
		Count  float64
	}
	RunningAge, CleanupAge, Pending, Failures, CleanupAttempts float64
	Duration                                                   struct {
		Count   uint64
		Sum     float64
		Buckets json.RawMessage
	}
}

type exportMetrics struct {
	ctx                                                        context.Context
	database                                                   *db.DB
	now                                                        func() time.Time
	running, duration, pending, failed, runningAge, cleanupAge *prometheus.Desc
}

// NewExportMetrics reads durable state shared by API and worker processes.
func NewExportMetrics(ctx context.Context, database *db.DB) prometheus.Collector {
	return &exportMetrics{ctx: ctx, database: database, now: time.Now,
		running:    prometheus.NewDesc("export_running", "Reserved or executing exports by tenant.", []string{"tenant"}, nil),
		duration:   prometheus.NewDesc("export_duration_seconds", "Completed export attempt duration, persisted with cleanup events.", nil, nil),
		pending:    prometheus.NewDesc("export_cleanup_pending", "Unacknowledged cleanup obligations, including exhausted retries.", nil, nil),
		failed:     prometheus.NewDesc("export_failed_total", "Persisted failed export attempts.", nil, nil),
		runningAge: prometheus.NewDesc("export_running_age_seconds", "Oldest executing export age.", nil, nil),
		cleanupAge: prometheus.NewDesc("export_cleanup_age_seconds", "Oldest unacknowledged cleanup age.", nil, nil),
	}
}

func (m *exportMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{m.running, m.duration, m.pending, m.failed, m.runningAge, m.cleanupAge} {
		ch <- desc
	}
}

func (m *exportMetrics) Collect(ch chan<- prometheus.Metric) {
	snapshot, err := m.snapshot(m.ctx)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(m.running, errors.New("export metrics query failed"))
		return
	}
	buckets := map[string]uint64{}
	if err := json.Unmarshal(snapshot.Duration.Buckets, &buckets); err != nil {
		ch <- prometheus.NewInvalidMetric(m.duration, errors.New("export duration buckets invalid"))
		return
	}
	histogram := map[float64]uint64{}
	for _, bound := range exportDurationBuckets {
		histogram[float64(bound)] = buckets[strconv.Itoa(bound)]
	}
	ch <- prometheus.MustNewConstHistogram(m.duration, snapshot.Duration.Count, snapshot.Duration.Sum, histogram)
	for _, row := range snapshot.Running {
		ch <- prometheus.MustNewConstMetric(m.running, prometheus.GaugeValue, row.Count, strconv.FormatInt(row.Tenant, 10))
	}
	ch <- prometheus.MustNewConstMetric(m.pending, prometheus.GaugeValue, snapshot.Pending)
	ch <- prometheus.MustNewConstMetric(m.failed, prometheus.CounterValue, snapshot.Failures)
	ch <- prometheus.MustNewConstMetric(m.runningAge, prometheus.GaugeValue, snapshot.RunningAge)
	ch <- prometheus.MustNewConstMetric(m.cleanupAge, prometheus.GaugeValue, snapshot.CleanupAge)
}

var exportDurationBuckets = []int{1, 5, 10, 30, 60, 120, 180, 300, 600}

func (m *exportMetrics) snapshot(ctx context.Context) (result exportMetricSnapshot, err error) {
	if err := catalogQuery("Export.Metrics"); err != nil {
		return result, err
	}
	q, cancel := m.database.WithContext(ctx)
	defer cancel()
	now := m.now().UTC()
	// Include inactive tenants with zero running slots so the gauge remains visible.
	if err := q.Table("(SELECT id FROM tenant UNION SELECT tenant_id AS id FROM export_job) t").
		Joins("LEFT JOIN export_job j ON j.tenant_id=t.id").Select("t.id AS tenant, count(j.id) FILTER (WHERE j.status='running') AS count").Group("t.id").Scan(&result.Running).Error; err != nil {
		return result, err
	}
	if err := q.Table("export_job").Select("COALESCE(GREATEST(0,EXTRACT(EPOCH FROM (?::timestamptz-min(started_at)))),0)", now).
		Where("status='running'").Scan(&result.RunningAge).Error; err != nil {
		return result, err
	}
	var cleanup struct{ Pending, Age, Attempts float64 }
	if err := q.Table("outbox_event").Select("count(*) AS pending, COALESCE(GREATEST(0,EXTRACT(EPOCH FROM (?::timestamptz-min(created_at)))),0) AS age, COALESCE(max(attempts),0) AS attempts", now).
		Where("event_type='export.cleanup' AND status<>'sent'").Scan(&cleanup).Error; err != nil {
		return result, err
	}
	result.Pending, result.CleanupAge, result.CleanupAttempts = cleanup.Pending, cleanup.Age, cleanup.Attempts
	if err := q.Table("outbox_event").Select("count(*)").Where(`event_type='export.cleanup' AND payload @> '{"failed":true}'::jsonb`).Scan(&result.Failures).Error; err != nil {
		return result, err
	}
	parts := make([]string, 0, len(exportDurationBuckets))
	for _, bound := range exportDurationBuckets {
		parts = append(parts, fmt.Sprintf("'%d',count(*) FILTER (WHERE (payload->>'duration_seconds')::double precision<=%d)", bound, bound))
	}
	err = q.Table("outbox_event").Select("count(*) AS count, COALESCE(sum((payload->>'duration_seconds')::double precision),0) AS sum, jsonb_build_object(" + strings.Join(parts, ",") + ") AS buckets").
		Where("event_type='export.cleanup' AND payload ? 'duration_seconds'").Scan(&result.Duration).Error
	return result, err
}

package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/prometheus/client_golang/prometheus"
)

type credentialRefreshMetrics struct {
	ctx          context.Context
	database     *db.DB
	now          func() time.Time
	leadTime     time.Duration
	descriptions []*prometheus.Desc
}

// NewCredentialRefreshMetrics observes durable state across API and worker restarts.
// Aggregated series intentionally exclude credential, user, job and run identifiers.
func NewCredentialRefreshMetrics(ctx context.Context, database *db.DB, settings config.Refresh) prometheus.Collector {
	names := []struct{ name, help string }{
		{"credential_refresh_success_ratio", "Published logical rotations divided by decided rotations; unknown is unsuccessful."},
		{"credential_refresh_logical_total", "Decided logical rotations grouped by input credential version."},
		{"credential_refresh_attempts_total", "Actual durable send attempts, excluding intent and queue redeliveries."},
		{"credential_refresh_consecutive_failures", "Largest consecutive temporary failure count."},
		{"credential_refresh_refreshing_age_seconds", "Age of the oldest pending durable refresh phase."},
		{"credential_refresh_reconciliation_pending", "Unresolved unknown refresh outcomes."},
		{"credential_refresh_due_age_seconds", "Oldest overdue automatic maintenance deadline."},
		{"credential_refresh_expiring", "Credentials inside the configurable maintenance lead time."},
		{"credential_notification_pending", "Pending external maintenance notifications."},
		{"credential_notification_failed", "External maintenance notifications with exhausted or revoked delivery."},
	}
	result := &credentialRefreshMetrics{ctx: ctx, database: database, now: time.Now, leadTime: settings.MaintenanceLeadTime()}
	for _, item := range names {
		result.descriptions = append(result.descriptions, prometheus.NewDesc(item.name, item.help, nil, nil))
	}
	return result
}

func (m *credentialRefreshMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range m.descriptions {
		ch <- d
	}
}

func (m *credentialRefreshMetrics) Collect(ch chan<- prometheus.Metric) {
	values, err := m.snapshot(m.ctx)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(m.descriptions[0], errors.New("credential refresh metrics query failed"))
		return
	}
	for i, d := range m.descriptions {
		kind := prometheus.GaugeValue
		if i == 2 {
			kind = prometheus.CounterValue
		}
		ch <- prometheus.MustNewConstMetric(d, kind, values[i])
	}
}

func (m *credentialRefreshMetrics) snapshot(ctx context.Context) ([]float64, error) {
	q, cancel := m.database.WithContext(ctx)
	defer cancel()
	now := m.now().UTC()
	var logical struct{ Successful, Total, Attempts float64 }
	err := q.Raw(`WITH logical AS (SELECT tenant_id,credential_id,input_version_id,bool_or(phase='published') AS success,
 bool_or(phase='unknown' OR (status='failed' AND next_retry_at IS NULL)) OR min(started_at)<=? AS decided
 FROM credential_refresh_run WHERE status<>'skipped' GROUP BY tenant_id,credential_id,input_version_id)
 SELECT count(*) FILTER(WHERE success) AS successful,count(*) FILTER(WHERE decided OR success) AS total,
 (SELECT count(*) FROM credential_refresh_run WHERE phase<>'intent') AS attempts FROM logical`, now.Add(-2*time.Minute)).Scan(&logical).Error
	if err != nil {
		return nil, err
	}
	var state struct{ Failures, Age, Pending, Due, Expiring float64 }
	err = q.Raw(`SELECT COALESCE(max(consecutive_failures),0) AS failures,
 (SELECT COALESCE(GREATEST(0,EXTRACT(EPOCH FROM (?::timestamptz-min(started_at)))),0) FROM credential_refresh_run WHERE status='running') AS age,
 (SELECT count(*) FROM credential_refresh_reconciliation WHERE resolved_at IS NULL) AS pending,
 COALESCE(GREATEST(0,EXTRACT(EPOCH FROM (?::timestamptz-min(refresh_at) FILTER(WHERE refresh_strategy='auto' AND status IN ('active','expiring','refreshing'))))),0) AS due,
 count(*) FILTER(WHERE expires_at<=? AND status<>'disabled') AS expiring FROM shop_credential`, now, now, now.Add(m.leadTime)).Scan(&state).Error
	if err != nil {
		return nil, err
	}
	var delivery struct{ Pending, Failed float64 }
	err = q.Table("notification").Select("count(*) FILTER(WHERE status='pending') AS pending,count(*) FILTER(WHERE status='failed') AS failed").Where("credential_id IS NOT NULL AND channel<>'inbox'").Scan(&delivery).Error
	ratio := float64(0)
	if logical.Total > 0 {
		ratio = logical.Successful / logical.Total
	}
	return []float64{ratio, logical.Total, logical.Attempts, state.Failures, state.Age, state.Pending, state.Due, state.Expiring, delivery.Pending, delivery.Failed}, err
}

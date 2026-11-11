package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/prometheus/client_golang/prometheus"
)

type accountMetrics struct {
	ctx         context.Context
	database    *db.DB
	unavailable *prometheus.Desc
}

// NewAccountMetrics observes reset requests awaiting an available email channel.
func NewAccountMetrics(ctx context.Context, database *db.DB) prometheus.Collector {
	return &accountMetrics{ctx: ctx, database: database, unavailable: prometheus.NewDesc("password_reset_channel_unavailable",
		"Password reset deliveries awaiting an email channel.", nil, nil)}
}

func (m *accountMetrics) Describe(ch chan<- *prometheus.Desc) { ch <- m.unavailable }

func (m *accountMetrics) Collect(ch chan<- prometheus.Metric) {
	q, cancel := m.database.WithContext(m.ctx)
	defer cancel()
	var count int64
	if err := q.Table("notify_outbox").Where("template_code='identity.reset' AND status='channel_unavailable'").Count(&count).Error; err != nil {
		ch <- prometheus.NewInvalidMetric(m.unavailable, errors.New("account metrics query failed"))
		return
	}
	ch <- prometheus.MustNewConstMetric(m.unavailable, prometheus.GaugeValue, float64(count))
}

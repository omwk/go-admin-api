package sync

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics reads committed aggregates, including work completed by other processes.
type Metrics struct {
	ctx                     context.Context
	store                   repo.MetricsStore
	runs, items, lag, drift *prometheus.Desc
}

func NewMetrics(ctx context.Context, store repo.MetricsStore) *Metrics {
	return &Metrics{ctx: ctx, store: store,
		drift: prometheus.NewDesc("sync_drift_open", "Open schema drift events.", nil, nil),
		runs:  prometheus.NewDesc("sync_run_results", "Persisted runs by current result.", []string{"result"}, nil),
		items: prometheus.NewDesc("sync_item_results", "Persisted item dispositions.", []string{"result"}, nil),
		lag:   prometheus.NewDesc("sync_cursor_lag_seconds", "Maximum age of committed checkpoints by resource.", []string{"resource"}, nil)}
}
func (m *Metrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.runs
	ch <- m.items
	ch <- m.lag
	ch <- m.drift
}
func (m *Metrics) Collect(ch chan<- prometheus.Metric) {
	snapshot, err := m.store.Snapshot(m.ctx)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(m.runs, errors.New("sync metrics query failed"))
		return
	}
	for key, value := range snapshot.Runs {
		ch <- prometheus.MustNewConstMetric(m.runs, prometheus.GaugeValue, value, key)
	}
	for key, value := range snapshot.Items {
		ch <- prometheus.MustNewConstMetric(m.items, prometheus.GaugeValue, value, key)
	}
	for key, value := range snapshot.CursorLag {
		ch <- prometheus.MustNewConstMetric(m.lag, prometheus.GaugeValue, value, key)
	}
	ch <- prometheus.MustNewConstMetric(m.drift, prometheus.GaugeValue, snapshot.DriftOpen)
}

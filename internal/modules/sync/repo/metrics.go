package repo

import "context"

// MetricSnapshot contains only aggregate counts, without tenant or business data.
type MetricSnapshot struct{ Runs, Items, CursorLag map[string]float64 }
type MetricsStore interface {
	Snapshot(context.Context) (MetricSnapshot, error)
}

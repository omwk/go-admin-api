package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/prometheus/client_golang/prometheus"
)

type syncMetricsStore struct{ database *db.DB }

func NewSyncMetrics(ctx context.Context, database *db.DB) prometheus.Collector {
	return syncmodule.NewMetrics(ctx, &syncMetricsStore{database: database})
}

// Snapshot is a bootstrap-only, fixed aggregate projection for system metrics.
func (s *syncMetricsStore) Snapshot(ctx context.Context) (repo.MetricSnapshot, error) {
	result := repo.MetricSnapshot{Runs: map[string]float64{}, Items: map[string]float64{}, CursorLag: map[string]float64{}}
	if err := engineQuery("SyncMetrics.Snapshot"); err != nil {
		return result, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var runs []struct {
		Status string
		Count  float64
	}
	if err := q.Table("sync_run").Select("status,count(*) AS count").Group("status").Scan(&runs).Error; err != nil {
		return result, err
	}
	for _, row := range runs {
		result.Runs[row.Status] = row.Count
	}
	var items struct{ Created, Updated, Skipped, Quarantined, Failed float64 }
	if err := q.Table("sync_run").Select(`COALESCE(sum((stats->>'created')::bigint),0) AS created, COALESCE(sum((stats->>'updated')::bigint),0) AS updated, COALESCE(sum((stats->>'skipped')::bigint),0) AS skipped, COALESCE(sum((stats->>'quarantined')::bigint),0) AS quarantined, COALESCE(sum((stats->>'failed')::bigint),0) AS failed`).Scan(&items).Error; err != nil {
		return result, err
	}
	result.Items = map[string]float64{"created": items.Created, "updated": items.Updated, "skipped": items.Skipped, "quarantined": items.Quarantined, "failed": items.Failed}
	var cursors []struct {
		Resource string
		Lag      float64
	}
	if err := q.Table("sync_cursor").Select("resource_type AS resource,GREATEST(0,EXTRACT(EPOCH FROM (now()-min(updated_at)))) AS lag").Group("resource_type").Scan(&cursors).Error; err != nil {
		return result, err
	}
	for _, row := range cursors {
		result.CursorLag[row.Resource] = row.Lag
	}
	return result, nil
}

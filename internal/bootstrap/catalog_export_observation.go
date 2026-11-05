package bootstrap

import (
	"context"
	"log/slog"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
)

func exportAlertLevel(value, warning, critical float64) string {
	if value >= critical {
		return "critical"
	}
	if value >= warning {
		return "warning"
	}
	return "ok"
}

// observeExport logs threshold transitions only; raw errors and object keys are excluded.
func observeExport(ctx context.Context, log *slog.Logger, snapshot exportMetricSnapshot, previous map[string]string) {
	for _, check := range []struct {
		name                     string
		value, warning, critical float64
	}{
		{"export_duration", snapshot.RunningAge, 180, 300},
		{"export_cleanup_age", snapshot.CleanupAge, 300, 900},
		{"export_cleanup_retries", snapshot.CleanupAttempts, 3, 10},
	} {
		level := exportAlertLevel(check.value, check.warning, check.critical)
		if previous[check.name] == level {
			continue
		}
		previous[check.name] = level
		if level == "ok" {
			continue
		}
		severity := slog.LevelWarn
		if level == "critical" {
			severity = slog.LevelError
		}
		log.Log(ctx, severity, "export threshold exceeded", "module", "catalog", "action", check.name, "severity", level, "value", check.value)
	}
}

func pollExportObservation(ctx context.Context, database *db.DB, log *slog.Logger) {
	metrics := NewExportMetrics(ctx, database).(*exportMetrics)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	levels := map[string]string{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot, err := metrics.snapshot(ctx)
			if err != nil {
				if ctx.Err() == nil {
					log.ErrorContext(ctx, "export observation failed", "module", "catalog", "action", "export_observation")
				}
				continue
			}
			observeExport(ctx, log, snapshot, levels)
		}
	}
}

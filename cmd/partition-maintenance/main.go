// Command partition-maintenance prebuilds raw partitions using a dedicated
// maintenance login, at startup and daily. It does not run synchronization jobs.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"

	"github.com/omwk/go-admin-api/internal/bootstrap"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	log, err := logger.New(os.Stderr, "info", "json")
	if err != nil {
		os.Exit(1)
	}
	if err := run(ctx, log); err != nil && !errors.Is(err, context.Canceled) {
		log.ErrorContext(ctx, "raw partition maintenance stopped", "module", "ingestion", "action", "partition_maintenance")
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) (err error) {
	cfg, err := config.Load(os.Getenv("CH_CONFIG_PATH"))
	if err != nil {
		return err
	}
	// Mandatory override prevents accidentally using the application's DSN.
	cfg.DB.DSN = os.Getenv("CH_MAINTENANCE_DB_DSN")
	if cfg.DB.DSN == "" {
		return errors.New("CH_MAINTENANCE_DB_DSN is required")
	}
	database, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	maintenance, err := ingestion.NewPartitionMaintainer(bootstrap.NewRawPartitionStore(database),
		func(ctx context.Context, month ingestion.Month, _ error) {
			log.WarnContext(ctx, "raw partition coverage missing or maintenance failed", "module", "ingestion",
				"action", "partition_coverage", "month", month.String())
		})
	if err != nil {
		return err
	}
	return maintenance.Run(ctx)
}

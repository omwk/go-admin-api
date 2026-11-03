package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/omwk/go-admin-api/internal/bootstrap"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) (err error) {
	cfg, err := config.Load(os.Getenv("CH_CONFIG_PATH"))
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	log, err := logger.New(os.Stdout, cfg.Log.Level, cfg.Log.Format)
	if err != nil {
		return err
	}
	database, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	shared, err := cache.Open(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, shared.Close()) }()
	return bootstrap.RunWorker(ctx, cfg, database, shared, log)
}

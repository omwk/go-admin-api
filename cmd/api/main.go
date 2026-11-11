package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/bootstrap"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	version   = "dev"
	buildTime = "unknown"
	commit    = "unknown"
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
	redis, err := cache.Open(ctx, cfg.Redis, cfg.Cache)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, redis.Close()) }()
	gin.SetMode(gin.ReleaseMode)
	system := &httpx.SystemAuthentication{}
	router, err := httpx.NewEngine(httpx.Dependencies{DB: database, Redis: redis, Log: log,
		SystemAuth: system, TrustedProxies: cfg.App.TrustedProxies,
		Collectors: []prometheus.Collector{bootstrap.NewSyncMetrics(ctx, database), bootstrap.NewExportMetrics(ctx, database), bootstrap.NewCredentialRefreshMetrics(ctx, database, cfg.Refresh), bootstrap.NewAccountMetrics(ctx, database)},
		Build:      httpx.BuildInfo{Version: version, BuildTime: buildTime, Commit: commit}})
	if err != nil {
		return err
	}
	closeIdentity, err := bootstrap.RegisterIdentity(ctx, router, database, redis, cfg, system)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeIdentity()) }()
	open, closeOpen, err := bootstrap.NewOpenGateway(ctx, database, cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeOpen()) }()
	server := &http.Server{
		Addr: cfg.App.HTTPAddr, Handler: gateway.Dispatch(open, httpx.Timeout(router, 30*time.Second)),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: cfg.App.ReadTimeout,
		WriteTimeout: cfg.App.WriteTimeout + time.Second, IdleTimeout: 60 * time.Second,
	}
	log.InfoContext(ctx, "API started", "module", "http", "action", "listen")
	return httpx.Serve(ctx, server)
}

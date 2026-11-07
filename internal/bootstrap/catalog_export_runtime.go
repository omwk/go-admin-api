package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
)

func newExportRuntime(database *db.DB, cfg *config.Config) (*catalog.ExportService, error) {
	object, err := storage.NewS3(cfg.Storage)
	if err != nil {
		return nil, err
	}
	service, err := catalog.NewExportService(NewExportStore(database, cfg.Storage.Bucket), object, newAdminStore(database).Authorize)
	if err != nil {
		return nil, err
	}
	if err := useOrderExportFormat(service); err != nil {
		return nil, err
	}
	return service, nil
}

func registerExportRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, cfg *config.Config) error {
	service, err := newExportRuntime(database, cfg)
	if err != nil {
		return err
	}
	return catalog.RegisterExportRoutes(router, auth, profiles, service)
}

func exportEventHandler(service *catalog.ExportService, fallback func(context.Context, outbox.Event) error) func(context.Context, outbox.Event) error {
	return func(ctx context.Context, event outbox.Event) error {
		if event.EventType != "export.dispatch" && event.EventType != "export.cleanup" {
			return fallback(ctx, event)
		}
		var payload struct {
			ExportID int64  `json:"export_id"`
			Key      string `json:"key"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil || event.TenantID <= 0 || payload.ExportID <= 0 ||
			event.AggregateType != "export_job" || event.AggregateID != strconv.FormatInt(payload.ExportID, 10) {
			return errors.New("invalid export event identity")
		}
		if event.EventType == "export.dispatch" {
			return service.Execute(ctx, event.TenantID, payload.ExportID)
		}
		if payload.Key == "" {
			return service.RetryCleanup(ctx, event.TenantID, payload.ExportID, "")
		}
		tenant, id, _, valid := catalog.ParseExportObjectKey(payload.Key)
		if !valid || tenant != event.TenantID || id != payload.ExportID {
			return errors.New("invalid export cleanup key")
		}
		return service.RetryCleanup(ctx, event.TenantID, payload.ExportID, payload.Key)
	}
}

func pollExportCleanup(ctx context.Context, service *catalog.ExportService, log *slog.Logger) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := service.Cleanup(ctx, 0, 0); err != nil && ctx.Err() == nil {
				log.ErrorContext(ctx, "export cleanup failed", "module", "catalog", "action", "export_cleanup")
			}
			timer.Reset(5 * time.Minute)
		}
	}
}

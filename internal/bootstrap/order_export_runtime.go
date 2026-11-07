package bootstrap

import (
	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/order"
)

func useOrderExportFormat(service *catalog.ExportService) error {
	return service.UseFormat("orders", catalog.ExportFormat{Header: order.ExportCSVHeader(), Record: func(row catalog.ExportRow) ([]string, error) {
		return order.ExportCSVRecord(row.Projection)
	}})
}

func registerOrderExportRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, cfg *config.Config) error {
	files, err := newExportRuntime(database, cfg)
	if err != nil {
		return err
	}
	service, err := order.NewExportService(&exportStore{database: database, bucket: cfg.Storage.Bucket}, files, (&orderQueryStore{database: database}).Authorize)
	if err != nil {
		return err
	}
	return order.RegisterExportRoutes(router, auth, profiles, service)
}

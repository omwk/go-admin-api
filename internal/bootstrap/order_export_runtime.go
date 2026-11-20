package bootstrap

import (
	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/infra/storage"
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

func registerOrderExportRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, resolver *storage.Resolver) error {
	files, err := newExportRuntime(database, resolver)
	if err != nil {
		return err
	}
	service, err := order.NewExportService(&exportStore{database: database}, files, (&orderQueryStore{database: database}).Authorize)
	if err != nil {
		return err
	}
	return order.RegisterExportRoutes(router, auth, profiles, service)
}

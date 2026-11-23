package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"gorm.io/gorm/clause"
)

// SeedGatewayRoutes installs missing built-in routes for the configured platform tenant.
// Existing rows, including tenant edits and disabled routes, are never updated.
func SeedGatewayRoutes(ctx context.Context, database *db.DB, platformTenantID int64) error {
	if platformTenantID <= 0 {
		return errors.New("gateway seed platform tenant is required")
	}
	route := gateway.Route{TenantID: platformTenantID, Code: "usage.read", Name: "用量查询",
		Capability: "usage.read", Method: "GET", Path: "/open/v1/usage", RouteGroup: "usage",
		Visibility: "public", Status: "enabled", BillingMode: "free", UnitsPerCall: 1, Currency: "CNY", Version: 1}
	q, cancel := database.WithContext(ctx)
	defer cancel()
	if err := q.Table("api_route").Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "code"}}, DoNothing: true,
	}).Create(&route).Error; err != nil {
		return fmt.Errorf("seed gateway routes: %w", err)
	}
	return nil
}

package bootstrap

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/modules/order"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"gorm.io/gorm"
)

type productResourceHandler struct{}

func (productResourceHandler) Validate(mapped ingestion.Mapped) string {
	return syncmodule.ValidateProductRecord(mapped)
}

func (productResourceHandler) Upsert(ctx context.Context, tx *gorm.DB, mapped ingestion.Mapped) (ingestion.UpsertStats, error) {
	return (catalog.Upserter{}).Upsert(ctx, tx, ingestion.ResourceProduct, []ingestion.Mapped{mapped})
}

func (productResourceHandler) OnCycleComplete(context.Context, *gorm.DB, syncmodule.ResourceCycle) error {
	return nil
}

type categoryResourceHandler struct{}

func (categoryResourceHandler) Validate(mapped ingestion.Mapped) string {
	name, ok := mapped.Values["name"].(string)
	if !ok {
		return "type_mismatch"
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(mapped.ExternalID) == "" {
		return "missing_required"
	}
	if ext, exists := mapped.Values["ext"]; exists {
		values, ok := ext.(map[string]any)
		if !ok {
			return "type_mismatch"
		}
		if parent, exists := values["parent_external_id"]; exists && parent != nil {
			if _, ok := parent.(string); !ok {
				return "type_mismatch"
			}
		}
	}
	return ""
}

func (categoryResourceHandler) Upsert(ctx context.Context, tx *gorm.DB, mapped ingestion.Mapped) (ingestion.UpsertStats, error) {
	return (catalog.CategoryUpserter{}).Upsert(ctx, tx, mapped)
}

func (categoryResourceHandler) OnCycleComplete(ctx context.Context, tx *gorm.DB, cycle syncmodule.ResourceCycle) error {
	if cycle.TenantID <= 0 || cycle.PlatformID <= 0 || cycle.StartedAt.IsZero() {
		return errors.New("category cycle identity is incomplete")
	}
	return tx.WithContext(ctx).Model(&catalog.PlatformCategory{}).
		Where("tenant_id=? AND platform_id=? AND synced_at < ?", cycle.TenantID, cycle.PlatformID, cycle.StartedAt).
		Update("synced_at", cycle.StartedAt.Add(-time.Nanosecond)).Error
}

func registeredResourceHandlers(writers ...*order.Upserter) map[string]syncmodule.ResourceHandler {
	var writer *order.Upserter
	if len(writers) > 0 {
		writer = writers[0]
	}
	return map[string]syncmodule.ResourceHandler{
		string(connector.ResourceProduct):  productResourceHandler{},
		string(connector.ResourceCategory): categoryResourceHandler{},
		string(connector.ResourceOrder):    order.ResourceHandler{Writer: writer},
	}
}

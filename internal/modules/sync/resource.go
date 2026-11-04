package sync

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"gorm.io/gorm"
)

// ResourceCycle describes a successfully completed non-empty resource cycle.
type ResourceCycle struct {
	TenantID   int64
	PlatformID int64
	ShopID     int64
	StartedAt  time.Time
}

// ResourceHandler owns validation, canonical persistence, and cycle completion for one resource.
type ResourceHandler interface {
	Validate(ingestion.Mapped) string
	Upsert(context.Context, *gorm.DB, ingestion.Mapped) (ingestion.UpsertStats, error)
	OnCycleComplete(context.Context, *gorm.DB, ResourceCycle) error
}

// ValidateProductRecord validates the current product and SKU canonical fields.
func ValidateProductRecord(mapped ingestion.Mapped) string {
	if reason := validateProduct(mapped.Values); reason != "" {
		return reason
	}
	return validateChildren(mapped.Collections["skus"])
}

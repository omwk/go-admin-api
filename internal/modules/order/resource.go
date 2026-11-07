package order

import (
	"context"
	"errors"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"gorm.io/gorm"
)

// ResourceHandler adapts frozen canonical mappings to the aggregate writer.
type ResourceHandler struct{ Writer *Upserter }

// Validate rejects unknown targets and lossy amount conversions before persistence.
func (ResourceHandler) Validate(m ingestion.Mapped) string {
	_, _, err := mappedSnapshot(m)
	if err != nil {
		return err.Error()
	}
	return ""
}

// Upsert persists the aggregate and its visibility transition in the page transaction.
func (h ResourceHandler) Upsert(ctx context.Context, tx *gorm.DB, m ingestion.Mapped) (ingestion.UpsertStats, error) {
	s, header, err := mappedSnapshot(m)
	if err != nil {
		return ingestion.UpsertStats{}, err
	}
	if header.Deleted {
		if h.Writer == nil {
			return ingestion.UpsertStats{}, errors.New("order writer is required")
		}
		err := changeVisibility(ctx, tx, s, true, header.VanishReason, h.Writer.sealer)
		return ingestion.UpsertStats{Skipped: 1}, err
	}
	if h.Writer == nil {
		return ingestion.UpsertStats{}, errors.New("order writer is required")
	}
	var old Order
	err = tx.WithContext(ctx).Select("id,version").Where("tenant_id=? AND shop_id=? AND external_order_id=?", m.TenantID, m.ShopID, m.ExternalID).Take(&old).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return ingestion.UpsertStats{}, err
	}
	s.ExpectedVersion = old.Version
	result, err := h.Writer.Apply(ctx, tx, s)
	if err != nil {
		return ingestion.UpsertStats{}, err
	}
	stats := ingestion.UpsertStats{}
	switch result.Outcome {
	case "applied":
		if old.ID == 0 {
			stats.Inserted = 1
		} else {
			stats.Updated = 1
		}
	case "skipped_same":
		stats.Skipped, stats.SkippedSame = 1, 1
	case "skipped_stale":
		stats.Skipped, stats.SkippedStale = 1, 1
	default:
		stats.Quarantined, stats.Reason = 1, "invalid_value"
	}
	if !m.Replay && (result.Outcome == "applied" || result.Outcome == "skipped_same") {
		err = changeVisibility(ctx, tx, s, false, "", h.Writer.sealer)
	}
	return stats, err
}

// OnCycleComplete never infers deletion from absence in an incremental window.
func (ResourceHandler) OnCycleComplete(context.Context, *gorm.DB, syncmodule.ResourceCycle) error {
	return nil
}

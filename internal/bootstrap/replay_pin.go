package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"gorm.io/gorm"
)

// All online freeze/retirement paths use this advisory lock before reading raw.
func lockRawMaintenance(q *gorm.DB, shared bool) error {
	query := "SELECT pg_advisory_xact_lock(185018,8)"
	if shared {
		query = "SELECT pg_advisory_xact_lock_shared(185018,8)"
	}
	return q.Exec(query).Error
}

func releaseReplayPin(q *gorm.DB, id int64) error {
	// Retain pin history, frozen members and checkpoints through recovery.
	return q.Exec("UPDATE raw_partition_pin SET released_at=clock_timestamp() WHERE run_id=? AND released_at IS NULL", id).Error
}

// Retire serializes archive detach/drop with member freezing and rejects active pins.
// Detached payload must be archived by the caller before requesting drop.
func (s *rawPartitionStore) Retire(ctx context.Context, month ingestion.Month, drop bool) error {
	if !month.Valid() {
		return errors.New("invalid partition month")
	}
	return s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		if err := lockRawMaintenance(q, false); err != nil {
			return err
		}
		start, _ := month.Bounds()
		var pins int64
		if err := q.Table("raw_partition_pin").Where("month=? AND released_at IS NULL", start).Count(&pins).Error; err != nil {
			return err
		}
		if pins > 0 {
			return ingestion.ErrPartitionPinned
		}
		// The identifier is derived exclusively from the validated Month value.
		table := "raw_payload_" + start.Format("200601")
		if drop {
			return q.Exec("DROP TABLE " + table).Error
		}
		return q.Exec("ALTER TABLE raw_payload DETACH PARTITION " + table).Error
	})
}

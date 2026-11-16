package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
)

// PruneAudit removes only rows strictly older than the explicit maintenance cutoff.
func PruneAudit(ctx context.Context, database *db.DB, before time.Time) (int64, error) {
	var count int64
	err := database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := database.WithContext(ctx)
		defer cancel()
		if err := q.Exec("SELECT set_config('commercehub.audit_prune_before', ?, true)", before.UTC().Format(time.RFC3339Nano)).Error; err != nil {
			return err
		}
		result := q.Exec("DELETE FROM audit_log WHERE created_at < ?", before.UTC())
		count = result.RowsAffected
		return result.Error
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

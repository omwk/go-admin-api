package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
)

type rawPartitionStore struct{ database *db.DB }

// NewRawPartitionStore uses a dedicated maintenance connection. The migration
// owner retains function execution; other maintenance roles need an explicit
// EXECUTE grant plus ownership/CREATE rights. Never grant these to the app role.
func NewRawPartitionStore(database *db.DB) ingestion.PartitionStore {
	return &rawPartitionStore{database: database}
}

func (s *rawPartitionStore) Ensure(ctx context.Context, month ingestion.Month) (bool, error) {
	if !month.Valid() {
		return false, errors.New("invalid partition month")
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var created bool
	err := q.Raw("SELECT ensure_raw_partition(?)", month.String()).Scan(&created).Error
	return created, err
}

func (s *rawPartitionStore) Covered(ctx context.Context, month ingestion.Month) (bool, error) {
	if !month.Valid() {
		return false, errors.New("invalid partition month")
	}
	start, end := month.Bounds()
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var covered bool
	err := q.Raw(`SELECT EXISTS (
        SELECT 1 FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE i.inhparent = 'raw_payload'::regclass AND n.nspname = current_schema()
          AND c.relname = ? AND c.relkind = 'r'
          AND pg_get_expr(c.relpartbound, c.oid) = format('FOR VALUES FROM (%L) TO (%L)', ?::timestamptz, ?::timestamptz)
    )`, "raw_payload_"+start.Format("200601"), start, end).Scan(&covered).Error
	return covered, err
}

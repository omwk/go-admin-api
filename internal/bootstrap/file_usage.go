package bootstrap

import (
	"context"
	"fmt"
)

func (s *fileStore) FileUsage(ctx context.Context, tenant, user int64) (int64, int64, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row struct{ UsedBytes, FileCount int64 }
	err := q.Table("file_object").Select("COALESCE(SUM(size_bytes),0) AS used_bytes, COUNT(*) AS file_count").Where("tenant_id=? AND uploaded_by=?", tenant, user).Scan(&row).Error
	return row.UsedBytes, row.FileCount, err
}

func (s *fileStore) LockFileUser(ctx context.Context, tenant, user int64) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", fmt.Sprintf("file:user:%d:%d", tenant, user)).Error
}

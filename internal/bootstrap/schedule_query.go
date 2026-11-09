package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/schedule/repo"
	"gorm.io/gorm"
)

type scheduleStore struct{ database *db.DB }

// NewScheduleStore exposes only the dedicated scheduler query port.
func NewScheduleStore(database *db.DB) repo.Store { return &scheduleStore{database: database} }

func (s *scheduleStore) ListEnabledJobs(ctx context.Context, tenantID int64) ([]repo.EnabledJob, error) {
	if s == nil || s.database == nil || tenantID < 0 {
		return nil, errors.New("schedule query dependencies are required")
	}
	if _, err := scopeRegistryQuery("Schedule.ListEnabledJobs"); err != nil {
		return nil, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []repo.EnabledJob{}
	q = q.Table("sync_job").Select("id AS job_id, cron").Where("enabled = ? AND credential_blocked_id IS NULL", true)
	// Zero selects the global bootstrap schedule projection, never business data.
	if tenantID > 0 {
		q = q.Where("tenant_id = ?", tenantID)
	}
	err := q.Order("id asc").Find(&rows).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []repo.EnabledJob{}, nil
	}
	return rows, err
}

var _ repo.Store = (*scheduleStore)(nil)

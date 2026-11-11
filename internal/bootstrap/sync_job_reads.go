package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
)

const syncJobReadColumns = `x.id,x.shop_id,x.resource_type,x.cron,x.enabled,x.last_run_id,
	r.status AS last_run_status,COALESCE(c.cursor_type,'') AS cursor_type,COALESCE(c.cursor_value,'') AS cursor_value,
	c.updated_at AS cursor_updated_at,x.created_at,x.updated_at`

func syncJobMetadata(q *gorm.DB) *gorm.DB {
	return q.Joins("LEFT JOIN sync_cursor c ON c.tenant_id=x.tenant_id AND c.shop_id=x.shop_id AND c.resource_type=x.resource_type").
		Joins("LEFT JOIN sync_run r ON r.tenant_id=x.tenant_id AND r.shop_id=x.shop_id AND r.job_id=x.id AND r.id=x.last_run_id")
}

func (s *syncEngineStore) ListJobs(ctx context.Context, p repo.PageQuery) (repo.Result, error) {
	q, cancel, err := s.humanQuery(ctx, "sync_job", "SyncAdmin.ListJobs")
	if err != nil {
		return repo.Result{}, err
	}
	defer cancel()
	if p.ShopID > 0 {
		q = q.Where("x.shop_id=?", p.ShopID)
	}
	if p.JobID > 0 {
		q = q.Where("x.id=?", p.JobID)
	}
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return repo.Result{}, err
	}
	rows := []repo.JobView{}
	err = syncJobMetadata(q).Select(syncJobReadColumns).Order("x.created_at DESC,x.id DESC").
		Limit(p.Size).Offset((p.Number - 1) * p.Size).Find(&rows).Error
	return repo.Result{Items: rows, Total: total, Page: p.Number, PageSize: p.Size}, err
}

func (s *syncEngineStore) ReadJob(ctx context.Context, id int64) (repo.JobView, error) {
	q, cancel, err := s.humanQuery(ctx, "sync_job", "SyncAdmin.ReadJob")
	if err != nil {
		return repo.JobView{}, err
	}
	defer cancel()
	var row repo.JobView
	err = syncJobMetadata(q).Select(syncJobReadColumns).Where("x.id=?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, syncMissing()
	}
	return row, err
}

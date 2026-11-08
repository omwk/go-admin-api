package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/share"
	"gorm.io/gorm"
)

func (s *shareStore) RecordVisit(ctx context.Context, row share.Link, result string, now time.Time) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return q.Table("share_visit").Create(map[string]any{"tenant_id": row.TenantID, "share_link_id": row.ID, "result": result, "created_at": now}).Error
}

func (s *shareStore) Visits(ctx context.Context, tenant, link int64, in share.Query) ([]share.Visit, int64, error) {
	if _, err := s.Link(ctx, tenant, link, true); err != nil {
		return nil, 0, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	q = q.Table("share_visit").Where("tenant_id=? AND share_link_id=?", tenant, link)
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []share.Visit{}
	err := q.Select("id,share_link_id,result,created_at").Order("created_at DESC,id DESC").Limit(in.PageSize).Offset((in.Page - 1) * in.PageSize).Find(&rows).Error
	return rows, total, err
}

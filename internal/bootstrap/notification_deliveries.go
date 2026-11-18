package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm"
)

func (m *notificationManager) ListDeliveries(ctx context.Context, a channel.Actor, page, size int, filter channel.DeliveryFilter) (any, error) {
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	query := q.Table("notify_delivery AS d").Where("d.tenant_id=?", a.TenantID)
	if filter.Status != "" {
		query = query.Where("d.status=?", filter.Status)
	}
	if filter.Channel != "" {
		query = query.Where("d.channel=?", filter.Channel)
	}
	if filter.TemplateCode != "" {
		query = query.Where("d.template_code=?", filter.TemplateCode)
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, notificationDBError(err)
	}
	// Pick one tenant override or global fallback locale deterministically.
	query = query.Joins(`LEFT JOIN LATERAL (
 SELECT name,subject FROM notify_template
 WHERE code=d.template_code AND (tenant_id=d.tenant_id OR tenant_id IS NULL)
 ORDER BY tenant_id NULLS LAST,(locale='zh-CN') DESC,locale,id LIMIT 1
 ) AS t ON true`)
	rows := []channel.DeliveryView{}
	err := query.Select("d." + deliveryColumnsWithPrefix() + ",coalesce(nullif(t.name,''),nullif(t.subject,''),d.template_code) AS template_name").
		Order("d.created_at DESC,d.id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error
	for i := range rows {
		rows[i].LastErrorMessage = channel.DeliveryErrorMessage(rows[i].LastError)
	}
	return channel.Page{List: rows, Total: total, Page: page, PageSize: size}, notificationDBError(err)
}

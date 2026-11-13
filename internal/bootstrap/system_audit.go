package bootstrap

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/system"
)

type systemAuditStore struct{ database *db.DB }

var _ system.AuditReader = (*systemAuditStore)(nil)

func (s *systemAuditStore) AuditRecords(ctx context.Context, tenant int64, filter system.AuditFilter) ([]system.AuditRecord, int64, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	q = q.Table("audit_log AS logs").Where("logs.tenant_id = ?", tenant)
	for _, item := range []struct {
		predicate string
		value     string
	}{
		{"logs.action = ?", filter.Action}, {"logs.resource_type = ?", filter.ResourceType},
		{"logs.resource_id = ?", filter.ResourceID}, {"logs.result = ?", filter.Result},
	} {
		if item.value != "" {
			q = q.Where(item.predicate, item.value)
		}
	}
	if filter.ActorID > 0 {
		q = q.Where("logs.actor_id = ?", filter.ActorID)
	}
	if filter.From != nil {
		q = q.Where("logs.created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		q = q.Where("logs.created_at <= ?", *filter.To)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	columns := `logs.id, logs.actor_type, logs.actor_id, logs.action, logs.resource_type, logs.resource_id,
		logs."before", logs."after", COALESCE(host(logs.ip),'') AS ip,
		logs.user_agent, logs.trace_id, logs.result, logs.created_at`
	if filter.Action == "identity.login" {
		q = q.Joins("LEFT JOIN sys_user AS users ON users.tenant_id = logs.tenant_id AND users.id = logs.actor_id")
		columns += ", COALESCE(users.username,'') AS username"
	}
	rows := []system.AuditRecord{}
	err := q.Select(columns).Order("logs.created_at DESC, logs.id DESC").
		Offset((filter.Page - 1) * filter.PageSize).Limit(filter.PageSize).Find(&rows).Error
	return rows, total, err
}

func registerSystemAuditRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB) error {
	service, err := system.NewAuditService(&systemAuditStore{database: database})
	if err != nil {
		return err
	}
	return system.RegisterAuditRoutes(router, auth, profiles, service)
}

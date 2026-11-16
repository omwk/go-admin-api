package bootstrap

import (
	"context"
	"strings"

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
	if filter.ActorName != "" {
		q = q.Where("logs.actor_name = ?", filter.ActorName)
	}
	if filter.ActionPrefix != "" {
		q = q.Where("logs.action LIKE ?", auditLike(filter.ActionPrefix)+"%")
	}
	if filter.Keyword != "" {
		pattern := "%" + auditLike(filter.Keyword) + "%"
		q = q.Where("(logs.actor_name ILIKE ? OR logs.resource_label ILIKE ? OR logs.action ILIKE ?)", pattern, pattern, pattern)
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
		logs.user_agent, logs.trace_id, logs.result, logs.created_at,
		logs.actor_name, logs.actor_tenant_name, logs.resource_label, logs.error_code, logs.error_message`
	if filter.Action == "identity.login" {
		columns += ", COALESCE(logs.actor_name,'') AS username"
	}
	rows := []system.AuditRecord{}
	err := q.Select(columns).Order("logs.created_at DESC, logs.id DESC").
		Offset((filter.Page - 1) * filter.PageSize).Limit(filter.PageSize).Find(&rows).Error
	return rows, total, err
}

func auditLike(value string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(value)
}

func registerSystemAuditRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB) error {
	service, err := system.NewAuditService(&systemAuditStore{database: database})
	if err != nil {
		return err
	}
	return system.RegisterAuditRoutes(router, auth, profiles, service)
}

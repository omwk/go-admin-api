package bootstrap

import (
	"context"
	"strconv"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/db"
)

type auditResource struct {
	table, column string
	global        bool
}

var auditResources = map[string]auditResource{
	"tenant": {"tenant", "name", true}, "tenant_template": {"tenant_template", "name", true},
	"user": {"sys_user", "username", false}, "dept": {"sys_dept", "name", false},
	"role": {"sys_role", "name", false}, "menu": {"sys_menu", "name", false},
	"permission": {"sys_permission", "name", false}, "dict": {"sys_dict", "type", false},
	"dict_item": {"sys_dict_item", "label", false}, "shop": {"shop", "name", false},
	"customer": {"customer", "name", false}, "price_list": {"price_list", "name", false}, "pricelist": {"price_list", "name", false},
	"share_link": {"share_link", "title", false}, "api_client": {"api_client", "name", false},
	"api_route": {"api_route", "name", false}, "pricing_plan": {"pricing_plan", "name", false},
	"product": {"product_spu", "title", false}, "category": {"category", "name", false},
}

// RecordAudit captures display names in the active business transaction before appending.
// Read projections consume these columns directly and never join the current entities.
func RecordAudit(ctx context.Context, database *db.DB, event audit.Event) error {
	if err := snapshotAuditActor(ctx, database, &event); err != nil {
		return err
	}
	if event.ResourceLabel == "" {
		event.ResourceLabel = audit.ResourceName(event.ResourceType, event.Before, event.After)
	}
	if event.ResourceLabel == "" {
		if err := snapshotAuditResource(ctx, database, &event); err != nil {
			return err
		}
	}
	return audit.Record(ctx, database, event)
}

func snapshotAuditActor(ctx context.Context, database *db.DB, event *audit.Event) error {
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var tenantCode string
	if err := q.Table("tenant").Select("code").Where("id = ?", event.TenantID).Scan(&tenantCode).Error; err != nil {
		return err
	}
	if event.ActorTenantName == "" {
		event.ActorTenantName = tenantCode
	}
	if event.ActorID == nil {
		if event.ActorType == "system" && event.Action != "identity.login" {
			event.ActorName = "system"
		}
		return nil
	}
	table, column := "sys_user", "username"
	if event.ActorType == "api_client" {
		table, column = "api_client", "name"
	}
	var currentName string
	if err := q.Table(table).Select(column).Where("tenant_id = ? AND id = ?", event.TenantID, *event.ActorID).Scan(&currentName).Error; err != nil {
		return err
	}
	if currentName != "" {
		event.ActorName = currentName
	}
	return nil
}

func snapshotAuditResource(ctx context.Context, database *db.DB, event *audit.Event) error {
	resource, found := auditResources[event.ResourceType]
	if !found {
		if event.ResourceType == "setting" {
			event.ResourceLabel = event.ResourceID
		}
		return nil
	}
	id, err := strconv.ParseInt(event.ResourceID, 10, 64)
	if err != nil || id <= 0 {
		return nil
	}
	q, cancel := database.WithContext(ctx)
	defer cancel()
	q = q.Table(resource.table).Select(resource.column).Where("id = ?", id)
	if !resource.global {
		q = q.Where("tenant_id = ?", event.TenantID)
	}
	return q.Scan(&event.ResourceLabel).Error
}

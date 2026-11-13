package bootstrap

import (
	"context"
	"fmt"
	"strconv"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

func (s *tenantStore) Snapshot(ctx context.Context, tenant int64) (model.TenantSnapshot, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	if err := q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", fmt.Sprintf("identity:management:%d", tenant)).Error; err != nil {
		return model.TenantSnapshot{}, err
	}
	snap := model.TenantSnapshot{Roles: []model.TemplateRole{}, Menus: []model.TemplateMenu{}, Permissions: []model.Permission{}}
	var roles []model.Role
	if err := q.Select(adminRoleColumns).Where("tenant_id=?", tenant).Order("id").Find(&roles).Error; err != nil {
		return snap, err
	}
	var menus []model.Menu
	if err := q.Select(adminMenuColumns).Where("tenant_id=?", tenant).Order("id").Find(&menus).Error; err != nil {
		return snap, err
	}
	if err := q.Select("id,tenant_id,code,name,module,type").Where("tenant_id=?", tenant).Order("id").Find(&snap.Permissions).Error; err != nil {
		return snap, err
	}
	keys := templateMenuKeys(menus)
	for _, row := range menus {
		item := model.TemplateMenu{Menu: row, Key: keys[row.ID], ParentKey: keys[row.ParentID]}
		item.ID, item.TenantID, item.ParentID = 0, 0, 0
		snap.Menus = append(snap.Menus, item)
	}
	for _, row := range roles {
		item, err := s.snapshotRole(ctx, tenant, row, keys)
		if err != nil {
			return snap, err
		}
		snap.Roles = append(snap.Roles, item)
	}
	for i := range snap.Permissions {
		snap.Permissions[i].ID, snap.Permissions[i].TenantID = 0, 0
	}
	return snap, nil
}

func templateMenuKeys(rows []model.Menu) map[int64]string {
	counts := map[string]int{}
	for _, row := range rows {
		if row.Path != nil {
			counts[*row.Path]++
		}
	}
	keys := map[int64]string{}
	for _, row := range rows {
		key := "@menu:" + strconv.FormatInt(row.ID, 10)
		if row.Path != nil && *row.Path != "" && counts[*row.Path] == 1 {
			key = *row.Path
		}
		keys[row.ID] = key
	}
	return keys
}

func (s *tenantStore) snapshotRole(ctx context.Context, tenant int64, row model.Role, keys map[int64]string) (model.TemplateRole, error) {
	item := model.TemplateRole{Code: row.Code, Name: row.Name, DataScope: row.DataScope, Remark: row.Remark, Status: row.Status, Permissions: []string{}, Menus: []string{}}
	q, cancel := s.query(ctx)
	defer cancel()
	err := q.Table("sys_role_permission rp").Select("p.code").Joins("JOIN sys_permission p ON p.tenant_id=rp.tenant_id AND p.id=rp.permission_id").Where("rp.tenant_id=? AND rp.role_id=?", tenant, row.ID).Order("p.code").Scan(&item.Permissions).Error
	if err != nil {
		return item, err
	}
	var ids []int64
	if err := q.Model(&model.RoleMenu{}).Where("tenant_id=? AND role_id=?", tenant, row.ID).Order("menu_id").Pluck("menu_id", &ids).Error; err != nil {
		return item, err
	}
	for _, id := range ids {
		item.Menus = append(item.Menus, keys[id])
	}
	return item, nil
}

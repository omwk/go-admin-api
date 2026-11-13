package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

func (s *tenantStore) Materialize(ctx context.Context, tenant int64, snap model.TenantSnapshot) (int64, []int64, error) {
	if len(snap.Permissions) > 0 {
		if err := s.SeedPermissions(ctx, tenant, snap.Permissions); err != nil {
			return 0, nil, err
		}
	}
	menus, err := s.materializeMenus(ctx, tenant, snap.Menus)
	if err != nil {
		return 0, nil, err
	}
	roles, err := s.materializeRoles(ctx, tenant, snap.Roles, menus)
	if err != nil {
		return 0, nil, err
	}
	dept, err := s.seedDepartment(ctx, tenant, identity.SeedDepartmentName)
	return dept, roles, err
}

func (s *tenantStore) materializeMenus(ctx context.Context, tenant int64, rows []model.TemplateMenu) (map[string]int64, error) {
	ids := map[string]int64{}
	remaining := append([]model.TemplateMenu{}, rows...)
	q, cancel := s.query(ctx)
	defer cancel()
	for len(remaining) > 0 {
		next := []model.TemplateMenu{}
		for _, item := range remaining {
			if item.Key == "" || ids[item.Key] != 0 {
				return nil, errors.New("invalid template menu key")
			}
			if item.ParentKey != "" && ids[item.ParentKey] == 0 {
				next = append(next, item)
				continue
			}
			row := item.Menu
			row.ID, row.TenantID, row.ParentID = 0, tenant, ids[item.ParentKey]
			if err := q.Create(&row).Error; err != nil {
				return nil, adminDBError(err)
			}
			ids[item.Key] = row.ID
		}
		if len(next) == len(remaining) {
			return nil, errors.New("invalid template menu graph")
		}
		remaining = next
	}
	return ids, nil
}

func (s *tenantStore) materializeRoles(ctx context.Context, tenant int64, rows []model.TemplateRole, menus map[string]int64) ([]int64, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var points []model.Permission
	if err := q.Select("id,code").Where("tenant_id=?", tenant).Find(&points).Error; err != nil {
		return nil, err
	}
	permissions := map[string]int64{}
	for _, row := range points {
		permissions[row.Code] = row.ID
	}
	ids, used := []int64{}, map[string]bool{}
	for _, item := range rows {
		code := item.Code
		for n := 2; used[code]; n++ {
			code = fmt.Sprintf("%s_%d", item.Code, n)
		}
		used[code] = true
		row := model.Role{TenantID: tenant, Code: code, Name: item.Name, DataScope: item.DataScope, Remark: item.Remark, Status: item.Status}
		if err := q.Create(&row).Error; err != nil {
			return nil, adminDBError(err)
		}
		spec := repo.SeedRole{Row: row, Permissions: item.Permissions, MenuPaths: item.Menus}
		if err := s.seedRoleGrants(ctx, tenant, row.ID, spec, permissions, menus); err != nil {
			return nil, err
		}
		if row.Status == "active" {
			ids = append(ids, row.ID)
		}
	}
	return ids, nil
}

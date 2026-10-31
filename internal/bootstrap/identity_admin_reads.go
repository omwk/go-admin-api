package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

func (s *adminStore) Users(ctx context.Context, tenant int64, page repo.Page) ([]model.User, int64, error) {
	q, cancel := s.query(ctx, tenant, &model.User{}, "Admin.Users")
	defer cancel()
	var total int64
	if err := q.Fork().Distinct("sys_user.id").Count(&total); err != nil {
		return nil, 0, err
	}
	order, ok := map[string]string{"-created_at": "created_at desc, id desc", "created_at": "created_at asc, id asc", "username": "username asc, id asc", "-username": "username desc, id desc"}[page.Sort]
	if !ok {
		return nil, 0, errors.New("unsupported user sort")
	}
	rows := []model.User{}
	err := q.Fork().Select(adminUserColumns).Distinct().Order(order).Limit(page.Size).Offset((page.Number - 1) * page.Size).Find(&rows)
	return rows, total, err
}

func (s *adminStore) Roles(ctx context.Context, tenant int64, page repo.Page) ([]model.Role, int64, error) {
	q, cancel := s.query(ctx, tenant, &model.Role{}, "Admin.Roles")
	defer cancel()
	var total int64
	if err := q.Fork().Count(&total); err != nil {
		return nil, 0, err
	}
	order, ok := map[string]string{"-id": "id desc", "id": "id asc", "code": "code asc, id asc", "-code": "code desc, id desc"}[page.Sort]
	if !ok {
		return nil, 0, errors.New("unsupported role sort")
	}
	rows := []model.Role{}
	err := q.Fork().Select(adminRoleColumns).Order(order).Limit(page.Size).Offset((page.Number - 1) * page.Size).Find(&rows)
	return rows, total, err
}

func (s *adminStore) Menus(ctx context.Context, tenant int64) ([]model.Menu, error) {
	q, cancel := s.query(ctx, tenant, &model.Menu{}, "Admin.Menus")
	defer cancel()
	rows := []model.Menu{}
	err := q.Select(adminMenuColumns).Order("sort asc, id asc").Find(&rows)
	return rows, err
}

func (s *adminStore) Permissions(ctx context.Context, tenant int64) ([]model.Permission, error) {
	q, cancel := s.query(ctx, tenant, &model.Permission{}, "Admin.Permissions")
	defer cancel()
	rows := []model.Permission{}
	err := q.Select("id, tenant_id, code, name, module, type").Order("module asc, code asc").Find(&rows)
	return rows, err
}

func (s *adminStore) References(ctx context.Context, tenant int64, resource string, id int64) (map[string]int64, error) {
	if err := requireAdminTenant(ctx, tenant, "Admin.References"); err != nil {
		return nil, err
	}
	var checks map[string]struct{ table, column string }
	switch resource {
	case "role":
		checks = map[string]struct{ table, column string }{"users": {"sys_user_role", "role_id"}, "menus": {"sys_role_menu", "role_id"}, "permissions": {"sys_role_permission", "role_id"}, "depts": {"sys_role_dept", "role_id"}}
	case "menu":
		checks = map[string]struct{ table, column string }{"children": {"sys_menu", "parent_id"}, "roles": {"sys_role_menu", "menu_id"}}
	default:
		return nil, errors.New("unsupported reference resource")
	}
	result := map[string]int64{}
	q, cancel := s.raw(ctx)
	defer cancel()
	if tenant <= 0 {
		return nil, errors.New("reference tenant is required")
	}
	for name, check := range checks {
		var count int64
		if err := q.Table(check.table).Where("tenant_id = ?", tenant).Where(check.column+" = ?", id).Count(&count).Error; err != nil {
			return nil, err
		}
		result[name] = count
	}
	return result, nil
}

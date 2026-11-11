package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

func (s *adminStore) ReadUser(ctx context.Context, tenant, id int64) (model.User, error) {
	q, cancel := s.query(ctx, tenant, &model.User{}, "Admin.ReadUser")
	defer cancel()
	var row model.User
	err := q.Select(adminUserColumns).Where("sys_user.id = ?", id).Take(&row)
	return row, adminDBError(err)
}

func (s *adminStore) ReadRole(ctx context.Context, tenant, id int64) (model.Role, error) {
	q, cancel := s.query(ctx, tenant, &model.Role{}, "Admin.ReadRole")
	defer cancel()
	var row model.Role
	err := q.Select(adminRoleColumns).Where("id = ?", id).Take(&row)
	return row, adminDBError(err)
}

func (s *adminStore) ReadMenu(ctx context.Context, tenant, id int64) (model.Menu, error) {
	q, cancel := s.query(ctx, tenant, &model.Menu{}, "Admin.ReadMenu")
	defer cancel()
	var row model.Menu
	err := q.Select(adminMenuColumns).Where("id = ?", id).Take(&row)
	return row, adminDBError(err)
}

func (s *adminStore) ReadLinks(ctx context.Context, tenant int64, kind repo.LinkKind, owner int64) ([]int64, error) {
	if err := requireAdminTenant(ctx, tenant, "Admin.ReadLinks"); err != nil {
		return nil, err
	}
	link, err := linkDefinition(kind)
	if err != nil {
		return nil, err
	}
	if kind == repo.UserRoles {
		_, err = s.ReadUser(ctx, tenant, owner)
	} else {
		_, err = s.ReadRole(ctx, tenant, owner)
	}
	if err != nil {
		return nil, err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	ids := []int64{}
	err = q.Table(link.table).Where("tenant_id = ?", tenant).Where(link.owner+" = ?", owner).
		Order(link.column).Pluck(link.column, &ids).Error
	return ids, err
}

func (s *adminStore) Departments(ctx context.Context, tenant, id int64) ([]repo.DepartmentRow, error) {
	if err := requireAdminTenant(ctx, tenant, "Admin.Departments"); err != nil {
		return nil, err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	q = q.Table("sys_dept").Select(`sys_dept.id, sys_dept.parent_id, sys_dept.name, sys_dept.path, sys_dept.sort, sys_dept.status,
		(SELECT COUNT(*) FROM sys_user dept_member WHERE dept_member.tenant_id = sys_dept.tenant_id
		AND dept_member.dept_id = sys_dept.id AND dept_member.deleted_at IS NULL) AS member_count`).
		Where("sys_dept.tenant_id = ?", tenant)
	if id > 0 {
		q = q.Where("sys_dept.id = ?", id)
	}
	rows := []repo.DepartmentRow{}
	err := q.Order("sys_dept.sort ASC, sys_dept.id ASC").Find(&rows).Error
	return rows, err
}

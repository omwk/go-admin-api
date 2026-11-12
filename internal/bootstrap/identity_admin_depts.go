package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

const adminDepartmentColumns = "sys_dept.id, sys_dept.tenant_id, sys_dept.parent_id, sys_dept.name, sys_dept.path, sys_dept.sort, sys_dept.status"

func (s *adminStore) LockDepartments(ctx context.Context, tenant int64) ([]model.Department, error) {
	q, cancel := s.query(ctx, tenant, &model.Department{}, "Admin.LockDepartments")
	defer cancel()
	rows := []model.Department{}
	err := q.Select(adminDepartmentColumns).Order("sys_dept.id ASC").Lock().Find(&rows)
	return rows, err
}

func (s *adminStore) CreateDepartment(ctx context.Context, row *model.Department) error {
	q, cancel := s.query(ctx, row.TenantID, row, "Admin.CreateDepartment")
	defer cancel()
	return adminDBError(q.Create(row))
}

func (s *adminStore) UpdateDepartment(ctx context.Context, tenant, id int64, values map[string]any) error {
	q, cancel := s.query(ctx, tenant, &model.Department{}, "Admin.UpdateDepartment")
	defer cancel()
	return scopedMutation(q, q.Where("sys_dept.id = ?", id).Updates(values))
}

func (s *adminStore) DeleteDepartment(ctx context.Context, tenant, id int64) error {
	q, cancel := s.query(ctx, tenant, &model.Department{}, "Admin.DeleteDepartment")
	defer cancel()
	return scopedMutation(q, q.Where("sys_dept.id = ?", id).Delete(&model.Department{}))
}

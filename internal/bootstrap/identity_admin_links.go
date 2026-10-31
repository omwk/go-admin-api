package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

type adminLink struct{ table, owner, column, target string }

func linkDefinition(kind repo.LinkKind) (adminLink, error) {
	links := map[repo.LinkKind]adminLink{
		repo.UserRoles:       {"sys_user_role", "user_id", "role_id", "sys_role"},
		repo.RoleMenus:       {"sys_role_menu", "role_id", "menu_id", "sys_menu"},
		repo.RolePermissions: {"sys_role_permission", "role_id", "permission_id", "sys_permission"},
		repo.RoleDepartments: {"sys_role_dept", "role_id", "dept_id", "sys_dept"},
	}
	link, ok := links[kind]
	if !ok {
		return adminLink{}, errors.New("unsupported authorization relationship")
	}
	return link, nil
}

func (s *adminStore) Links(ctx context.Context, tenant int64, kind repo.LinkKind, owner int64) ([]int64, error) {
	if err := s.linkOwner(ctx, tenant, kind, owner, "Admin.Links"); err != nil {
		return nil, err
	}
	link, err := linkDefinition(kind)
	if err != nil {
		return nil, err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	if tenant <= 0 || owner <= 0 {
		return nil, errors.New("authorization owner is required")
	}
	ids := []int64{}
	err = q.Table(link.table).Where("tenant_id = ?", tenant).Where(link.owner+" = ?", owner).Order(link.column).Pluck(link.column, &ids).Error
	return ids, err
}

func (s *adminStore) ReplaceLinks(ctx context.Context, tenant int64, kind repo.LinkKind, owner int64, ids []int64) error {
	if err := s.linkOwner(ctx, tenant, kind, owner, "Admin.ReplaceLinks"); err != nil {
		return err
	}
	link, err := linkDefinition(kind)
	if err != nil {
		return err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	if tenant <= 0 || owner <= 0 {
		return errors.New("authorization owner is required")
	}
	if len(ids) > 0 {
		var count int64
		if err := q.Table(link.target).Where("tenant_id = ? AND id IN ?", tenant, ids).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(ids)) {
			return errs.New(errs.ErrNotFound, 404, "授权目标不存在")
		}
	}
	if err := q.Table(link.table).Where("tenant_id = ?", tenant).Where(link.owner+" = ?", owner).Delete(map[string]any{}).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	rows := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, map[string]any{"tenant_id": tenant, link.owner: owner, link.column: id})
	}
	return adminDBError(q.Table(link.table).CreateInBatches(&rows, 500).Error)
}

func (s *adminStore) linkOwner(ctx context.Context, tenant int64, kind repo.LinkKind, owner int64, method string) error {
	if err := requireAdminTenant(ctx, tenant, method); err != nil {
		return err
	}
	if kind == repo.UserRoles {
		_, err := s.User(ctx, tenant, owner)
		return err
	}
	return nil
}

func (s *adminStore) loadGrantPolicy(ctx context.Context, tenant, user int64) (repo.GrantPolicy, error) {
	if tenant <= 0 || user <= 0 {
		return repo.GrantPolicy{}, errors.New("grant operator is required")
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	policy := repo.GrantPolicy{}
	if err := q.Table("sys_role r").Select("r.id, r.tenant_id, r.code, r.name, r.data_scope, r.status").
		Joins("JOIN sys_user_role ur ON ur.tenant_id = r.tenant_id AND ur.role_id = r.id").
		Where("r.tenant_id = ? AND ur.user_id = ? AND r.status = ?", tenant, user, "active").Find(&policy.Roles).Error; err != nil {
		return policy, err
	}
	var err error
	policy.MenuIDs, err = (&profileStore{query: s.raw}).RoleMenus(ctx, tenant, user)
	if err != nil {
		return policy, err
	}
	if err := q.Select("id, tenant_id, parent_id, name, path, sort, status").Where("tenant_id = ?", tenant).Find(&policy.Departments).Error; err != nil {
		return policy, err
	}
	ids := []int64{}
	for _, role := range policy.Roles {
		if role.DataScope == "custom" {
			ids = append(ids, role.ID)
		}
	}
	if len(ids) > 0 {
		err = q.Model(&model.RoleDepartment{}).Where("tenant_id = ? AND role_id IN ?", tenant, ids).Distinct().Pluck("dept_id", &policy.CustomDepartmentIDs).Error
	}
	return policy, err
}

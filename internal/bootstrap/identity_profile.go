package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
)

type profileStore struct {
	query func(context.Context) (*gorm.DB, context.CancelFunc)
}

var _ repo.Profile = (*profileStore)(nil)

func newProfileStore(database *db.DB) *profileStore {
	return &profileStore{query: database.WithContext}
}

func (s *profileStore) Identity(ctx context.Context, tenant, user int64) ([]repo.IdentityRow, error) {
	if tenant <= 0 || user <= 0 {
		return nil, errors.New("identity owner is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var rows []repo.IdentityRow
	err := q.Raw(`SELECT u.id, u.tenant_id, t.code AS tenant_code, u.username, u.nickname, u.avatar, u.dept_id AS department_id, u.is_super,
		r.id AS role_id, r.code AS role_code, r.name AS role_name, p.code AS permission_code, p.module AS permission_module
		FROM sys_user u JOIN tenant t ON t.id = u.tenant_id AND t.status = 'active'
		LEFT JOIN sys_user_role ur ON ur.tenant_id = u.tenant_id AND ur.user_id = u.id
		LEFT JOIN sys_role r ON r.tenant_id = ur.tenant_id AND r.id = ur.role_id AND r.status = 'active'
		LEFT JOIN sys_role_permission rp ON rp.tenant_id = r.tenant_id AND rp.role_id = r.id AND NOT u.is_super
		LEFT JOIN sys_permission p ON p.tenant_id = u.tenant_id AND (u.is_super OR p.id = rp.permission_id)
		WHERE u.tenant_id = ? AND u.id = ? AND u.deleted_at IS NULL AND u.status = 'active'`, tenant, user).Find(&rows).Error
	return rows, err
}

func (s *profileStore) Menus(ctx context.Context, tenant int64) ([]model.Menu, error) {
	if tenant <= 0 {
		return nil, errors.New("menu tenant is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var rows []model.Menu
	err := q.Select("id, tenant_id, parent_id, name, type, path, component, redirect, icon, sort, visible, keep_alive, is_external, permission_code, meta, status").
		Where("tenant_id = ? AND status = ? AND visible = ? AND type <> ?", tenant, "active", true, "button").Find(&rows).Error
	return rows, err
}

func (s *profileStore) RoleMenus(ctx context.Context, tenant, user int64) ([]int64, error) {
	if tenant <= 0 || user <= 0 {
		return nil, errors.New("menu owner is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	ids := []int64{}
	err := q.Table("sys_role_menu rm").Distinct("rm.menu_id").
		Joins("JOIN sys_user_role ur ON ur.tenant_id = rm.tenant_id AND ur.role_id = rm.role_id").
		Joins("JOIN sys_role r ON r.tenant_id = rm.tenant_id AND r.id = rm.role_id AND r.status = ?", "active").
		Where("rm.tenant_id = ? AND ur.user_id = ?", tenant, user).Pluck("rm.menu_id", &ids).Error
	return ids, err
}

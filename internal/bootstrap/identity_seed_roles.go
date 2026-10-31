package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm/clause"
)

func (s *seedStore) SeedRoles(ctx context.Context, tenant int64, roles []repo.SeedRole) error {
	if tenant <= 0 {
		return errors.New("seed tenant is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var permissions []model.Permission
	if err := q.Select("id, code").Where("tenant_id = ?", tenant).Find(&permissions).Error; err != nil {
		return err
	}
	var menus []model.Menu
	if err := q.Select("id, path").Where("tenant_id = ?", tenant).Find(&menus).Error; err != nil {
		return err
	}
	permissionIDs, menuIDs := map[string]int64{}, map[string]int64{}
	for _, row := range permissions {
		permissionIDs[row.Code] = row.ID
	}
	for _, row := range menus {
		if row.Path != nil {
			menuIDs[*row.Path] = row.ID
		}
	}
	for _, spec := range roles {
		if err := s.ensureSeedRole(ctx, tenant, spec, permissionIDs, menuIDs); err != nil {
			return err
		}
	}
	return nil
}

func (s *seedStore) ensureSeedRole(ctx context.Context, tenant int64, spec repo.SeedRole, permissions, menus map[string]int64) error {
	q, cancel := s.query(ctx)
	defer cancel()
	row := spec.Row
	row.TenantID = tenant
	created := q.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if created.Error != nil {
		return created.Error
	}
	if err := q.Select("id, data_scope").Where("tenant_id = ? AND code = ?", tenant, row.Code).Take(&row).Error; err != nil {
		return err
	}
	if row.Code == identity.SuperAdminRoleCode && row.DataScope != "all" {
		return errors.New("super_admin seed role must retain data_scope all")
	}
	if created.RowsAffected > 0 {
		return s.seedRoleGrants(ctx, tenant, row.ID, spec, permissions, menus)
	}
	return nil
}

func (s *seedStore) seedRoleGrants(ctx context.Context, tenant, role int64, spec repo.SeedRole, permissions, menus map[string]int64) error {
	q, cancel := s.query(ctx)
	defer cancel()
	permissionRows, menuRows := []model.RolePermission{}, []model.RoleMenu{}
	for _, code := range spec.Permissions {
		id, exists := permissions[code]
		if !exists {
			return errors.New("seed permission does not exist")
		}
		permissionRows = append(permissionRows, model.RolePermission{TenantID: tenant, RoleID: role, PermissionID: id})
	}
	for _, path := range spec.MenuPaths {
		id, exists := menus[path]
		if !exists {
			return errors.New("seed menu does not exist")
		}
		menuRows = append(menuRows, model.RoleMenu{TenantID: tenant, RoleID: role, MenuID: id})
	}
	if len(permissionRows) > 0 {
		if err := q.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(permissionRows, 500).Error; err != nil {
			return err
		}
	}
	if len(menuRows) > 0 {
		return q.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(menuRows, 500).Error
	}
	return nil
}

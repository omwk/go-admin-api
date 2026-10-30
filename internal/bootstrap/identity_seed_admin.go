package bootstrap

import (
	"context"
	"errors"
	"strconv"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *seedStore) SeedAdmin(ctx context.Context, tenant int64, spec repo.AdminSeed) error {
	if tenant <= 0 || spec.Username == "" || spec.PasswordHash == "" {
		return errors.New("seed administrator identity is required")
	}
	department, err := s.seedDepartment(ctx, tenant, spec.DepartmentName)
	if err != nil {
		return err
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var user model.User
	err = q.Select("id").Where("tenant_id = ? AND username = ?", tenant, spec.Username).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		user = model.User{TenantID: tenant, Username: spec.Username, PasswordHash: spec.PasswordHash, DepartmentID: department, IsSuper: true, Status: "active"}
		if err := q.Create(&user).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	var role model.Role
	if err := q.Select("id").Where("tenant_id = ? AND code = ?", tenant, spec.RoleCode).Take(&role).Error; err != nil {
		return err
	}
	return q.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.UserRole{TenantID: tenant, UserID: user.ID, RoleID: role.ID}).Error
}

func (s *seedStore) seedDepartment(ctx context.Context, tenant int64, name string) (int64, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var row model.Department
	err := q.Select("id").Where("tenant_id = ? AND parent_id = ? AND name = ?", tenant, 0, name).Take(&row).Error
	if err == nil {
		return row.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	row = model.Department{TenantID: tenant, Name: name, Status: "active", Path: ""}
	if err := q.Create(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, q.Model(&model.Department{}).Where("tenant_id = ? AND id = ?", tenant, row.ID).Update("path", strconv.FormatInt(row.ID, 10)).Error
}

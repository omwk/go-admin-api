package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type tenantStore struct{ *seedStore }

var _ repo.Tenant = (*tenantStore)(nil)

func newTenantStore(database *db.DB) *tenantStore {
	return &tenantStore{&seedStore{newAuthStore(database)}}
}

func (s *tenantStore) Tenant(ctx context.Context, id int64, write bool) (model.Tenant, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var row model.Tenant
	if write {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := q.Select("id,code,name,status,settings,provisioned_from,created_at,updated_at").Where("id=?", id).Take(&row).Error
	return row, adminDBError(err)
}

func (s *tenantStore) Tenants(ctx context.Context, owner int64, page repo.Page) ([]model.TenantSummary, int64, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	q = q.Model(&model.Tenant{})
	if owner != 0 {
		q = q.Where("id=?", owner)
	}
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []model.TenantSummary{}
	err := q.Select(`tenant.id,tenant.code,tenant.name,tenant.status,tenant.provisioned_from,tenant.created_at,tenant.updated_at,
 (SELECT count(*) FROM sys_user u WHERE u.tenant_id=tenant.id AND u.deleted_at IS NULL AND u.status='active' AND u.is_super) AS admin_active_count,
 (SELECT count(*) FROM sys_user u WHERE u.tenant_id=tenant.id AND u.deleted_at IS NULL) AS member_count`).
		Order("tenant.created_at DESC,tenant.id DESC").Offset((page.Number - 1) * page.Size).Limit(page.Size).Find(&rows).Error
	return rows, total, err
}

func (s *tenantStore) CreateTenant(ctx context.Context, row *model.Tenant) error {
	q, cancel := s.query(ctx)
	defer cancel()
	return adminDBError(q.Create(row).Error)
}

func (s *tenantStore) UpdateTenant(ctx context.Context, id int64, name, status string) error {
	q, cancel := s.query(ctx)
	defer cancel()
	return adminMutation(q.Model(&model.Tenant{}).Where("id=?", id).Updates(map[string]any{"name": name, "status": status}))
}

func (s *tenantStore) Template(ctx context.Context, id int64, write bool) (model.TenantTemplate, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	strength := "SHARE"
	if write {
		strength = "UPDATE"
	}
	var row model.TenantTemplate
	err := q.Select("id,code,name,description,roles,menus,permissions,settings,created_at,updated_at").Where("id=?", id).Clauses(clause.Locking{Strength: strength}).Take(&row).Error
	return row, adminDBError(err)
}

func (s *tenantStore) Templates(ctx context.Context, page repo.Page) ([]model.TenantTemplate, int64, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	q = q.Model(&model.TenantTemplate{})
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []model.TenantTemplate{}
	err := q.Select("id,code,name,description,roles,menus,permissions,settings,created_at,updated_at").Order("created_at DESC,id DESC").Offset((page.Number - 1) * page.Size).Limit(page.Size).Find(&rows).Error
	return rows, total, err
}

func (s *tenantStore) CreateTemplate(ctx context.Context, row *model.TenantTemplate) error {
	q, cancel := s.query(ctx)
	defer cancel()
	return adminDBError(q.Create(row).Error)
}

func (s *tenantStore) UpdateTemplate(ctx context.Context, id int64, name, description string) error {
	q, cancel := s.query(ctx)
	defer cancel()
	return adminMutation(q.Model(&model.TenantTemplate{}).Where("id=?", id).Updates(map[string]any{"name": name, "description": description}))
}

func (s *tenantStore) DeleteTemplate(ctx context.Context, id int64) error {
	q, cancel := s.query(ctx)
	defer cancel()
	return adminMutation(q.Where("id=?", id).Delete(&model.TenantTemplate{}))
}

func (s *tenantStore) TemplateUsed(ctx context.Context, id int64) (bool, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var total int64
	err := q.Model(&model.Tenant{}).Where("provisioned_from=?", id).Count(&total).Error
	return total > 0, err
}

func (s *tenantStore) CreateTenantAdmin(ctx context.Context, user *model.User, roles []int64, token *model.AuthToken) error {
	q, cancel := s.query(ctx)
	defer cancel()
	if err := q.Create(user).Error; err != nil {
		return adminDBError(err)
	}
	for _, role := range roles {
		if err := q.Create(&model.UserRole{TenantID: user.TenantID, UserID: user.ID, RoleID: role}).Error; err != nil {
			return err
		}
	}
	token.UserID, token.CreatedBy = &user.ID, user.ID
	return q.Create(token).Error
}

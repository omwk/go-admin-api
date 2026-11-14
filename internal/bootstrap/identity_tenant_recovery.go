package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
)

func (s *authStore) LockInviteTenant(ctx context.Context, tenant int64) error {
	q, cancel := s.query(ctx)
	defer cancel()
	// Serialize invitations without upgrading the tenant SHARE lock used by login.
	return q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", fmt.Sprintf("identity:invite:%d", tenant)).Error
}

func (s *authStore) HasActiveTenantAdmin(ctx context.Context, tenant int64) (bool, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var count int64
	err := q.Model(&model.User{}).Where("tenant_id=? AND status='active' AND is_super=true", tenant).Count(&count).Error
	return count > 0, err
}

func (s *tenantStore) TenantMembers(ctx context.Context, tenant int64, page repo.Page) ([]model.TenantMember, int64, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	q = q.Model(&model.User{}).Where("tenant_id=?", tenant)
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []model.TenantMember{}
	err := q.Select("id,username,nickname,status,is_super,created_at").Order("created_at DESC,id DESC").
		Offset((page.Number - 1) * page.Size).Limit(page.Size).Find(&rows).Error
	return rows, total, err
}

func (s *tenantStore) TenantInviteTarget(ctx context.Context, tenant int64) (model.AuthToken, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var user model.User
	err := q.Select("id,username,dept_id").Where("tenant_id=? AND is_super=true", tenant).
		Order("CASE WHEN status='active' THEN 0 ELSE 1 END,id").Take(&user).Error
	if err == nil {
		return model.AuthToken{TenantID: tenant, UserID: &user.ID, Username: &user.Username, DepartmentID: &user.DepartmentID, CreatedBy: user.ID}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return model.AuthToken{}, err
	}
	// Preserve the token's tenant-local creator FK; the real platform actor is audited separately.
	err = q.Unscoped().Select("id").Where("tenant_id=?", tenant).Order("id").Take(&user).Error
	if err != nil {
		return model.AuthToken{}, adminDBError(err)
	}
	// If all administrators were removed, acceptance elects the new first administrator.
	var department model.Department
	err = q.Select("id").Where("tenant_id=? AND status='active'", tenant).Order("parent_id,id").Take(&department).Error
	return model.AuthToken{TenantID: tenant, DepartmentID: &department.ID, CreatedBy: user.ID}, adminDBError(err)
}

func (s *tenantStore) ReplaceTenantInvite(ctx context.Context, token *model.AuthToken, now time.Time) error {
	q, cancel := s.query(ctx)
	defer cancel()
	if err := q.Model(&model.AuthToken{}).Where("tenant_id=? AND purpose='invite' AND used_at IS NULL AND revoked_at IS NULL", token.TenantID).
		Update("revoked_at", now).Error; err != nil {
		return err
	}
	return adminDBError(q.Create(token).Error)
}

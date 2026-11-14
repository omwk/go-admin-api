package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ repo.Registration = (*accountStore)(nil)
var _ repo.RegistrationStatus = (*authStore)(nil)

func (s *accountStore) RegistrationUser(ctx context.Context, tenant int64, account, email string) (model.User, bool, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var row model.User
	err := q.Select(loginColumns).Where("tenant_id=? AND (username=? OR lower(email)=?)", tenant, account, email).
		Order("id").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, false, nil
	}
	return row, err == nil, err
}

func (s *accountStore) RegistrationDepartment(ctx context.Context, tenant int64) (int64, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var row model.Department
	err := q.Select("id").Where("tenant_id=? AND parent_id=0 AND status='active'", tenant).Order("id").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, errs.New(errs.ErrConflict, 409, "注册配置不可用")
	}
	return row.ID, err
}

func (s *accountStore) CreateRegistrationUser(ctx context.Context, user *model.User) error {
	err := s.CreateInvitedUser(ctx, user, nil)
	var app *errs.AppError
	if errors.As(err, &app) && app.Code == errs.ErrConflict {
		return repo.ErrRegistrationCollision
	}
	return err
}

func (s *accountStore) RegistrationRoles(ctx context.Context, tenant int64, code string) ([]int64, error) {
	if code == "" {
		return []int64{}, nil
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var row model.Role
	err := q.Select("id").Where("tenant_id=? AND code=? AND status='active'", tenant, code).
		Clauses(clause.Locking{Strength: "SHARE"}).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errs.New(errs.ErrConflict, 409, "注册配置不可用")
	}
	if err != nil {
		return nil, err
	}
	return []int64{row.ID}, nil
}

func (s *accountStore) VerificationByHash(ctx context.Context, hash string) (model.AuthToken, bool, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var row model.AuthToken
	// The globally unique digest is the capability; subsequent reads are tenant scoped and locked.
	err := q.Select(lifecycleColumns).Where("purpose='verify' AND token_hash=?", hash).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, false, nil
	}
	return row, err == nil, err
}

func (s *authStore) HasPendingRegistration(ctx context.Context, tenant, user int64) (bool, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var count int64
	err := q.Model(&model.AuthToken{}).Where("tenant_id=? AND user_id=? AND purpose='verify' AND used_at IS NULL AND revoked_at IS NULL", tenant, user).Count(&count).Error
	return count > 0, err
}

func (s *accountStore) RevokeVerificationTokens(ctx context.Context, tenant, user int64, now time.Time) error {
	q, cancel := s.query(ctx)
	defer cancel()
	return q.Model(&model.AuthToken{}).Where("tenant_id=? AND user_id=? AND purpose='verify' AND used_at IS NULL AND revoked_at IS NULL", tenant, user).
		Update("revoked_at", now).Error
}

func (s *accountStore) ActivateRegisteredUser(ctx context.Context, user model.User, roles []int64) error {
	q, cancel := s.query(ctx)
	defer cancel()
	result := q.Model(&model.User{}).Where("tenant_id=? AND id=? AND status='disabled'", user.TenantID, user.ID).
		Updates(map[string]any{"status": "active", "is_super": user.IsSuper})
	if err := adminMutation(result); err != nil {
		return err
	}
	for _, role := range roles {
		if err := q.Create(&model.UserRole{TenantID: user.TenantID, UserID: user.ID, RoleID: role}).Error; err != nil {
			return err
		}
	}
	return bindFirstSuperRole(q, user)
}

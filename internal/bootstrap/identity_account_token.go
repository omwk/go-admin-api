package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const lifecycleColumns = "id,tenant_id,user_id,purpose,token_hash,expires_at,used_at,attempts,created_by,created_at,dept_id,role_ids,username,max_uses,used_count,revoked_at"

func (s *accountStore) InsertToken(ctx context.Context, row *model.AuthToken) error {
	q, cancel := s.query(ctx)
	defer cancel()
	return adminDBError(q.Create(row).Error)
}

func (s *accountStore) TokenByHash(ctx context.Context, tenant int64, purpose, hash string) (model.AuthToken, bool, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var row model.AuthToken
	err := q.Select(lifecycleColumns).Where("tenant_id=? AND purpose=? AND token_hash=?", tenant, purpose, hash).
		Clauses(clause.Locking{Strength: "UPDATE"}).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, false, nil
	}
	return row, err == nil, err
}

func (s *accountStore) TokenByID(ctx context.Context, tenant, id int64) (model.AuthToken, bool, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var row model.AuthToken
	err := q.Select(lifecycleColumns).Where("tenant_id=? AND id=?", tenant, id).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, false, nil
	}
	return row, err == nil, err
}

func (s *accountStore) SaveToken(ctx context.Context, row model.AuthToken) error {
	q, cancel := s.query(ctx)
	defer cancel()
	return adminMutation(q.Model(&model.AuthToken{}).Where("tenant_id=? AND id=?", row.TenantID, row.ID).Updates(map[string]any{
		"used_at": row.UsedAt, "used_count": row.UsedCount, "attempts": row.Attempts, "revoked_at": row.RevokedAt, "token_hash": row.TokenHash,
	}))
}

func (s *accountStore) Invites(ctx context.Context, tenant, creator int64, page repo.Page) ([]model.AuthToken, int64, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	query := q.Model(&model.AuthToken{}).Where("tenant_id=? AND purpose='invite'", tenant)
	if creator > 0 {
		query = query.Where("created_by=?", creator)
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []model.AuthToken{}
	err := query.Select(lifecycleColumns).Order("created_at DESC,id DESC").Offset((page.Number - 1) * page.Size).Limit(page.Size).Find(&rows).Error
	return rows, total, err
}

func (s *accountStore) RevokeResetTokens(ctx context.Context, tenant, user int64, now time.Time) error {
	q, cancel := s.query(ctx)
	defer cancel()
	return q.Model(&model.AuthToken{}).Where("tenant_id=? AND user_id=? AND purpose='reset' AND used_at IS NULL AND revoked_at IS NULL", tenant, user).Update("revoked_at", now).Error
}

func (s *accountStore) CreateInvitedUser(ctx context.Context, user *model.User, roles []int64) error {
	q, cancel := s.query(ctx)
	defer cancel()
	var count int64
	if err := q.Model(&model.Department{}).Where("tenant_id=? AND id=? AND status='active'", user.TenantID, user.DepartmentID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return errors.New("invitation department unavailable")
	}
	if len(roles) > 0 {
		if err := q.Model(&model.Role{}).Where("tenant_id=? AND id IN ? AND status='active'", user.TenantID, roles).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(roles)) {
			return errors.New("invitation roles unavailable")
		}
	}
	if err := q.Create(user).Error; err != nil {
		return adminDBError(err)
	}
	for _, role := range roles {
		if err := q.Create(&model.UserRole{TenantID: user.TenantID, UserID: user.ID, RoleID: role}).Error; err != nil {
			return adminDBError(err)
		}
	}
	return bindFirstSuperRole(q, *user)
}

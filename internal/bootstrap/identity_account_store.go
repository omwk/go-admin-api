package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
)

type accountStore struct{ *authStore }

var _ repo.Account = (*accountStore)(nil)

func (s *authStore) LockAccount(ctx context.Context, tenant, user int64) error {
	q, cancel := s.query(ctx)
	defer cancel()
	return q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", fmt.Sprintf("identity:account:%d:%d", tenant, user)).Error
}

func (s *accountStore) PasswordHistory(ctx context.Context, tenant, user int64) ([]model.PasswordHistory, error) {
	size, err := s.historySize(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return []model.PasswordHistory{}, nil
	}
	q, cancel := s.query(ctx)
	defer cancel()
	rows := []model.PasswordHistory{}
	err = q.Select("id,tenant_id,user_id,password_hash,created_at").Where("tenant_id=? AND user_id=?", tenant, user).Order("id DESC").Limit(size).Find(&rows).Error
	return rows, err
}

func (s *accountStore) RememberPassword(ctx context.Context, row model.PasswordHistory) error {
	size, err := s.historySize(ctx, row.TenantID)
	if err != nil {
		return err
	}
	q, cancel := s.query(ctx)
	defer cancel()
	if size == 0 {
		return q.Exec("DELETE FROM password_history WHERE tenant_id=? AND user_id=?", row.TenantID, row.UserID).Error
	}
	if err := q.Create(&row).Error; err != nil {
		return err
	}
	return q.Exec(`DELETE FROM password_history WHERE tenant_id=? AND user_id=? AND id NOT IN
 (SELECT id FROM password_history WHERE tenant_id=? AND user_id=? ORDER BY id DESC LIMIT ?)`, row.TenantID, row.UserID, row.TenantID, row.UserID, size).Error
}

func (s *accountStore) UpdatePassword(ctx context.Context, user model.User) error {
	q, cancel := s.query(ctx)
	defer cancel()
	return adminMutation(q.Model(&model.User{}).Where("tenant_id=? AND id=?", user.TenantID, user.ID).Updates(map[string]any{
		"password_hash": user.PasswordHash, "password_changed_at": user.PasswordChangedAt, "force_password_change": user.ForcePasswordChange,
		"failed_attempts": 0, "locked_until": nil, "status": user.Status,
	}))
}

func (s *accountStore) UpdateProfile(ctx context.Context, user model.User) error {
	q, cancel := s.query(ctx)
	defer cancel()
	result := q.Model(&model.User{}).Where("tenant_id=? AND id=?", user.TenantID, user.ID).Updates(map[string]any{
		"nickname": user.Nickname, "email": user.Email, "phone": user.Phone, "avatar": user.Avatar,
	})
	var postgres *pgconn.PgError
	if errors.As(result.Error, &postgres) && postgres.Code == "23505" && postgres.ConstraintName == "uq_sys_user_phone" {
		return errs.New(errs.ErrConflict, 409, "该手机号已被使用")
	}
	return adminMutation(result)
}

func (s *accountStore) OwnSessions(ctx context.Context, tenant, user int64, page repo.Page, now time.Time) ([]repo.OwnSession, int64, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	query := q.Model(&model.RefreshToken{}).Where("tenant_id=? AND user_id=? AND revoked_at IS NULL AND expires_at>?", tenant, user, now).
		Where(`NOT EXISTS(SELECT 1 FROM sys_refresh_token terminal WHERE terminal.tenant_id=? AND terminal.user_id=?
  AND terminal.session_id=sys_refresh_token.session_id AND terminal.revoked_reason IN ('logout','kick','replay'))`, tenant, user)
	var total int64
	if err := query.Session(&gorm.Session{}).Distinct("session_id").Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []repo.OwnSession{}
	err := query.Select(`session_id AS sid,user_id,MIN(created_at) AS created_at,MAX(expires_at) AS expires_at,
  host((ARRAY_AGG(ip ORDER BY created_at DESC,id DESC))[1]) AS ip,
  NULLIF((ARRAY_AGG(user_agent ORDER BY created_at DESC,id DESC))[1], '') AS user_agent`).
		Group("session_id,user_id").Order("created_at DESC,sid DESC").Offset((page.Number - 1) * page.Size).Limit(page.Size).Find(&rows).Error
	return rows, total, err
}

func (s *accountStore) SessionIDs(ctx context.Context, tenant, user int64) ([]string, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	ids := []string{}
	err := q.Model(&model.RefreshToken{}).Where("tenant_id=? AND user_id=?", tenant, user).Distinct("session_id").Order("session_id").Pluck("session_id", &ids).Error
	return ids, err
}

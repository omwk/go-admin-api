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

var _ repo.Reauthentication = (*authStore)(nil)

func (s *authStore) UserForReauth(ctx context.Context, tenant, userID int64) (model.User, bool, error) {
	if tenant <= 0 || userID <= 0 {
		return model.User{}, false, errors.New("reauthentication owner is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var user model.User
	err := q.Select(loginColumns).
		Joins("JOIN tenant ON tenant.id = sys_user.tenant_id AND tenant.status = ?", "active").
		Where("sys_user.tenant_id = ? AND sys_user.id = ?", tenant, userID).
		Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "sys_user"}}).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return user, false, nil
	}
	return user, err == nil, err
}

func (s *authStore) SaveReauth(ctx context.Context, tenant, user int64, sid string, at time.Time) error {
	if !validOwner(tenant, user, sid) || at.IsZero() {
		return errors.New("reauthentication session and time are required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	result := q.Model(&model.RefreshToken{}).Where("tenant_id=? AND user_id=? AND session_id=?", tenant, user, sid).
		Update("reauth_at", at)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("reauthentication session is unavailable")
	}
	return nil
}

func (s *authStore) ReauthAt(ctx context.Context, tenant, user int64, sid string, now time.Time) (*time.Time, error) {
	if !validOwner(tenant, user, sid) || now.IsZero() {
		return nil, errors.New("reauthentication session and time are required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var state struct{ ReauthAt *time.Time }
	err := q.Raw(`SELECT max(r.reauth_at) AS reauth_at FROM sys_refresh_token r
 JOIN sys_user u ON u.tenant_id=r.tenant_id AND u.id=r.user_id AND u.status='active' AND u.deleted_at IS NULL
 JOIN tenant t ON t.id=r.tenant_id AND t.status='active'
 WHERE r.tenant_id=? AND r.user_id=? AND r.session_id=? AND r.revoked_at IS NULL AND r.expires_at>?
 AND NOT EXISTS (SELECT 1 FROM sys_refresh_token terminal WHERE terminal.tenant_id=? AND terminal.user_id=?
 AND terminal.session_id=? AND terminal.revoked_reason IN ('logout','kick','replay'))`, tenant, user, sid, now, tenant, user, sid).Scan(&state).Error
	return state.ReauthAt, err
}

// Package bootstrap assembles application dependencies and dedicated infrastructure adapters.
package bootstrap

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const loginColumns = "sys_user.id, sys_user.tenant_id, sys_user.username, sys_user.password_hash, sys_user.nickname, sys_user.email, sys_user.phone, sys_user.avatar, sys_user.dept_id, sys_user.is_super, sys_user.created_at, sys_user.updated_at, sys_user.password_changed_at, sys_user.force_password_change, sys_user.status, sys_user.failed_attempts, sys_user.locked_until, sys_user.last_login_at, sys_user.last_login_ip"
const refreshColumns = "id, tenant_id, user_id, session_id, token_hash, replaced_by_token_id, revoked_reason, expires_at, revoked_at, ip, user_agent, created_at, reauth_at"

type authStore struct {
	transaction func(context.Context, func(context.Context) error) error
	query       func(context.Context) (*gorm.DB, context.CancelFunc)
}

var _ repo.Auth = (*authStore)(nil)

func newAuthStore(database *db.DB) *authStore {
	return &authStore{transaction: database.Transaction, query: database.WithContext}
}

func (s *authStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	return s.transaction(ctx, fn)
}

func (s *authStore) UserForLogin(ctx context.Context, tenant int64, username string) (model.User, bool, error) {
	if tenant <= 0 || username == "" {
		return model.User{}, false, errors.New("login tenant and username are required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var user model.User
	column, value, normalizeErr := identity.AccountIdentifier(username)
	if normalizeErr != nil {
		return user, false, nil
	}
	predicate := "sys_user.username = ?"
	if column == "email" {
		predicate = "lower(sys_user.email) = ?"
	}
	if column == "phone" {
		predicate = "sys_user.phone = ?"
	}
	err := q.Select(loginColumns).
		Joins("JOIN tenant ON tenant.id = sys_user.tenant_id AND tenant.status = ?", "active").
		Where("sys_user.tenant_id = ? AND "+predicate, tenant, value).
		Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "sys_user"}}).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return user, false, nil
	}
	return user, err == nil, err
}

func (s *authStore) SaveLogin(ctx context.Context, user model.User) error {
	if user.TenantID <= 0 || user.ID <= 0 {
		return errors.New("login identity is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	return q.Model(&model.User{}).Where("tenant_id = ? AND id = ?", user.TenantID, user.ID).
		Updates(map[string]any{"status": user.Status, "failed_attempts": user.FailedAttempts, "locked_until": user.LockedUntil, "last_login_at": user.LastLoginAt, "last_login_ip": user.LastLoginIP}).Error
}

func (s *authStore) RefreshByHash(ctx context.Context, hash string) (model.RefreshToken, bool, error) {
	digest, err := hex.DecodeString(hash)
	if err != nil || len(digest) != 32 {
		return model.RefreshToken{}, false, errors.New("refresh digest is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var row model.RefreshToken
	err = q.Select(refreshColumns).Where("token_hash = ?", hash).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, false, nil
	}
	return row, err == nil, err
}

func (s *authStore) LockSession(ctx context.Context, tenant int64, sid string) error {
	if tenant <= 0 || sid == "" {
		return errors.New("session tenant and identifier are required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	key := fmt.Sprintf("identity:session:%d:%s", tenant, sid)
	return q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key).Error
}

func (s *authStore) Family(ctx context.Context, tenant, user int64, sid string) (repo.Family, error) {
	if !validOwner(tenant, user, sid) {
		return repo.Family{}, errors.New("session ownership is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var rows []model.RefreshToken
	err := q.Select(refreshColumns).Where("tenant_id = ? AND user_id = ? AND session_id = ?", tenant, user, sid).Find(&rows).Error
	return repo.Family{Tokens: rows}, err
}

func (s *authStore) InsertRefresh(ctx context.Context, row *model.RefreshToken) error {
	if row.TenantID <= 0 || row.UserID <= 0 || row.SessionID == "" {
		return errors.New("refresh ownership is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	return q.Create(row).Error
}

func (s *authStore) ClaimRefresh(ctx context.Context, old model.RefreshToken, now time.Time) (bool, error) {
	if old.ID <= 0 || !validOwner(old.TenantID, old.UserID, old.SessionID) {
		return false, errors.New("refresh identity is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	result := q.Model(&model.RefreshToken{}).
		Where("tenant_id = ? AND user_id = ? AND session_id = ? AND id = ? AND revoked_at IS NULL AND expires_at > ?", old.TenantID, old.UserID, old.SessionID, old.ID, now).
		Updates(map[string]any{"revoked_at": now, "revoked_reason": "rotated"})
	return result.RowsAffected == 1, result.Error
}

func (s *authStore) LinkRefresh(ctx context.Context, old model.RefreshToken, replacement int64) error {
	if old.ID <= 0 || replacement <= 0 || !validOwner(old.TenantID, old.UserID, old.SessionID) {
		return errors.New("refresh replacement identity is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	return q.Model(&model.RefreshToken{}).
		Where("tenant_id = ? AND user_id = ? AND session_id = ? AND id = ? AND revoked_reason = ?", old.TenantID, old.UserID, old.SessionID, old.ID, "rotated").
		Update("replaced_by_token_id", replacement).Error
}

func (s *authStore) RevokeFamily(ctx context.Context, tenant, user int64, sid, reason string, now time.Time) error {
	if !validOwner(tenant, user, sid) {
		return errors.New("session ownership is required")
	}
	if reason != "logout" && reason != "kick" && reason != "replay" {
		return errors.New("invalid family revocation reason")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	return q.Model(&model.RefreshToken{}).Where("tenant_id = ? AND user_id = ? AND session_id = ?", tenant, user, sid).
		Updates(map[string]any{"revoked_at": gorm.Expr("COALESCE(revoked_at, ?)", now), "revoked_reason": reason}).Error
}

func validOwner(tenant, user int64, sid string) bool { return tenant > 0 && user > 0 && sid != "" }

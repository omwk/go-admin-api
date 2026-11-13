package identity

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
)

// ChangePassword checks the current password and retains exactly the caller's session.
func (s *AccountService) ChangePassword(ctx context.Context, claims jwt.Claims, oldPassword, newPassword string) error {
	if err := validatePassword(ctx, s.Settings, claims.TenantID, newPassword); err != nil {
		return err
	}
	candidate, err := s.owner(ctx, claims)
	if err != nil {
		return err
	}
	if pwd.Verify(candidate.PasswordHash, oldPassword) != nil {
		return invalidCredentials()
	}
	hash, err := pwd.Hash(newPassword)
	if err != nil {
		return accountError(err)
	}
	var marks []revocation
	err = s.Store.Transaction(ctx, func(tx context.Context) error {
		if err := s.Store.LockAccount(tx, candidate.TenantID, candidate.ID); err != nil {
			return err
		}
		current, err := s.owner(tx, claims)
		if err != nil {
			return err
		}
		if current.PasswordHash != candidate.PasswordHash {
			return invalidCredentials()
		}
		family, err := s.Auth.store.Family(tx, current.TenantID, current.ID, claims.SessionID)
		if err != nil {
			return err
		}
		if !activeReauthFamily(family, s.Now().UTC()) {
			return revokedToken()
		}
		if err := s.writePassword(tx, &current, newPassword, hash, false); err != nil {
			return err
		}
		marks, err = s.revokeSessions(tx, current, claims.SessionID)
		if err != nil {
			return err
		}
		return s.Auth.reauthAudit(tx, current.TenantID, current.ID, s.Now().UTC(), nil)
	})
	if err != nil {
		return accountError(err)
	}
	return s.Auth.mark(ctx, marks)
}

func invalidPassword() error { return invalidAdmin() }

func (s *AccountService) writePassword(ctx context.Context, user *model.User, password, hash string, force bool) error {
	policy, err := setting.LoadSecurity(ctx, s.Settings, user.TenantID)
	if err != nil {
		return dependencyFailure(err)
	}
	rows, err := s.Store.PasswordHistory(ctx, user.TenantID, user.ID)
	if err != nil {
		return err
	}
	if pwd.Verify(user.PasswordHash, password) == nil {
		return invalidPassword()
	}
	for i, row := range rows {
		if i >= policy.PasswordHistorySize {
			break
		}
		if row.PasswordHash != user.PasswordHash && pwd.Verify(row.PasswordHash, password) == nil {
			return invalidPassword()
		}
	}
	now := s.Now().UTC()
	if len(rows) == 0 {
		if err := s.Store.RememberPassword(ctx, model.PasswordHistory{TenantID: user.TenantID, UserID: user.ID, PasswordHash: user.PasswordHash, CreatedAt: now}); err != nil {
			return err
		}
	}
	user.PasswordHash, user.PasswordChangedAt, user.ForcePasswordChange = hash, &now, force
	if user.Status == "locked" {
		user.Status = "active"
	}
	if err := s.Store.UpdatePassword(ctx, *user); err != nil {
		return err
	}
	if err := s.Store.RevokeResetTokens(ctx, user.TenantID, user.ID, now); err != nil {
		return err
	}
	return s.Store.RememberPassword(ctx, model.PasswordHistory{TenantID: user.TenantID, UserID: user.ID, PasswordHash: hash, CreatedAt: now})
}

// AdminResetPassword applies the same history policy and forces a personal password change.
func (s *AccountService) AdminResetPassword(ctx context.Context, tenant, userID int64, password string) error {
	if err := validatePassword(ctx, s.Settings, tenant, password); err != nil {
		return err
	}
	hash, err := pwd.Hash(password)
	if err != nil {
		return accountError(err)
	}
	return s.Store.Transaction(ctx, func(tx context.Context) error {
		if err := s.Store.LockAccount(tx, tenant, userID); err != nil {
			return err
		}
		user, found, err := s.Store.UserForReauth(tx, tenant, userID)
		if err != nil {
			return err
		}
		if !found {
			return adminNotFound()
		}
		if err := s.writePassword(tx, &user, password, hash, true); err != nil {
			return err
		}
		_, err = s.revokeSessions(tx, user, "")
		return err
	})
}

func rememberInitialPassword(ctx context.Context, s *AccountService, user model.User, now time.Time) error {
	return s.Store.RememberPassword(ctx, model.PasswordHistory{TenantID: user.TenantID, UserID: user.ID, PasswordHash: user.PasswordHash, CreatedAt: now})
}

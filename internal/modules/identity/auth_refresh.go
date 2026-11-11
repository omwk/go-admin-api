package identity

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/session"
)

// Refresh rotates a token once; DB state always decides grace or family replay.
func (s *AuthService) Refresh(ctx context.Context, raw string, meta RequestMeta) (Tokens, error) {
	if err := s.redis.Ping(ctx); err != nil {
		return Tokens{}, dependencyFailure(err)
	}
	old, found, err := s.store.RefreshByHash(ctx, refresh.Hash(raw))
	if err != nil {
		return Tokens{}, dependencyFailure(err)
	}
	if !found || raw == "" {
		return Tokens{}, revokedToken()
	}
	var tokens Tokens
	var outcome error
	var marks []revocation
	err = s.store.Transaction(ctx, func(txCtx context.Context) error {
		if err := s.lockAccount(txCtx, old.TenantID, old.UserID); err != nil {
			return err
		}
		if err := s.store.LockSession(txCtx, old.TenantID, old.SessionID); err != nil {
			return err
		}
		family, err := s.store.Family(txCtx, old.TenantID, old.UserID, old.SessionID)
		if err != nil {
			return err
		}
		for _, row := range family.Tokens {
			if row.ID == old.ID {
				old.ReauthAt = row.ReauthAt
			}
		}
		now := s.now().UTC()
		if familyRevoked(family) {
			outcome = revokedToken()
			return nil
		}
		if !old.ExpiresAt.After(now) {
			outcome = expiredToken()
			return nil
		}
		claimed, err := s.store.ClaimRefresh(txCtx, old, now)
		if err != nil {
			return err
		}
		if !claimed {
			marks, outcome, err = s.reusedRefresh(txCtx, old, family, now)
			return err
		}
		tokens, err = s.finishRotation(txCtx, old, meta, now)
		if err == nil {
			marks = []revocation{{key: session.RefreshKey(old.TokenHash), expires: old.ExpiresAt}}
		}
		return err
	})
	if err != nil {
		return Tokens{}, dependencyFailure(err)
	}
	if err := s.mark(ctx, marks); err != nil {
		return Tokens{}, err
	}
	if outcome != nil {
		return Tokens{}, outcome
	}
	return tokens, nil
}

func (s *AuthService) finishRotation(ctx context.Context, old model.RefreshToken, meta RequestMeta, now time.Time) (Tokens, error) {
	tokens, next, err := s.issue(old.TenantID, old.UserID, old.SessionID, meta, now)
	if err != nil {
		return Tokens{}, err
	}
	if s.accounts != nil {
		user, found, err := s.accounts.Store.UserForReauth(ctx, old.TenantID, old.UserID)
		if err != nil {
			return Tokens{}, err
		}
		if found {
			tokens.PasswordStatus = passwordRestriction(user, now)
			tokens.PasswordExpired = tokens.PasswordStatus == "password_expired"
			tokens.PasswordChangeRequired = tokens.PasswordStatus != ""
		}
	}
	next.ReauthAt = old.ReauthAt
	if err := s.store.InsertRefresh(ctx, &next); err != nil {
		return Tokens{}, err
	}
	if err := s.store.LinkRefresh(ctx, old, next.ID); err != nil {
		return Tokens{}, err
	}
	return tokens, nil
}

func (s *AuthService) reusedRefresh(ctx context.Context, old model.RefreshToken, family repo.Family, now time.Time) ([]revocation, error, error) {
	row, found, err := s.store.RefreshByHash(ctx, old.TokenHash)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, revokedToken(), nil
	}
	if row.RevokedReason != nil && *row.RevokedReason == "expired" {
		return nil, expiredToken(), nil
	}
	if row.RevokedReason == nil || *row.RevokedReason != "rotated" || row.RevokedAt == nil {
		return nil, revokedToken(), nil
	}
	if now.Sub(*row.RevokedAt) <= 30*time.Second {
		return nil, refreshInFlight(), nil
	}
	if err := s.store.RevokeFamily(ctx, old.TenantID, old.UserID, old.SessionID, "replay", now); err != nil {
		return nil, nil, err
	}
	return familyMarks(family, old.SessionID), revokedToken(), nil
}

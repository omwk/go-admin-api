package identity

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/session"
)

// ReauthResult identifies the current session's short password-verification window.
type ReauthResult struct {
	ReauthAt  time.Time `json:"reauth_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Reauthenticate verifies the current account password without creating a new session.
func (s *AuthService) Reauthenticate(ctx context.Context, claims jwt.Claims, password string, meta RequestMeta) (ReauthResult, error) {
	user, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || user <= 0 || claims.TenantID <= 0 || claims.SessionID == "" {
		return ReauthResult{}, revokedToken()
	}
	if password == "" || len(password) > 1024 {
		return ReauthResult{}, errs.New(errs.ErrInvalidParam, 400, "重认证参数无效")
	}
	store, ok := s.store.(repo.Reauthentication)
	if !ok {
		return ReauthResult{}, dependencyFailure(errors.New("reauthentication persistence unavailable"))
	}
	allowed, err := s.redis.AllowLogin(ctx, meta.IP)
	if err != nil {
		return ReauthResult{}, dependencyFailure(err)
	}
	if !allowed {
		if err := s.reauthAudit(ctx, claims.TenantID, user, time.Time{}, errs.New(errs.ErrTooManyRequests, 429, "重认证请求过多")); err != nil {
			return ReauthResult{}, dependencyFailure(err)
		}
		return ReauthResult{}, errs.New(errs.ErrTooManyRequests, 429, "重认证请求过多")
	}
	candidate, found, err := store.UserForReauth(ctx, claims.TenantID, user)
	if err != nil {
		return ReauthResult{}, dependencyFailure(err)
	}
	hash := s.dummyHash
	if found {
		hash = candidate.PasswordHash
	}
	matches := s.verify(hash, password) == nil && found
	var result ReauthResult
	var outcome error
	err = s.store.Transaction(ctx, func(ctx context.Context) error {
		var err error
		result, outcome, err = s.commitReauth(ctx, store, claims, user, candidate, matches)
		return err
	})
	if err != nil {
		return ReauthResult{}, dependencyFailure(err)
	}
	return result, outcome
}

func (s *AuthService) commitReauth(ctx context.Context, store repo.Reauthentication, claims jwt.Claims, userID int64, candidate model.User, matches bool) (ReauthResult, error, error) {
	if err := s.lockAccount(ctx, claims.TenantID, userID); err != nil {
		return ReauthResult{}, nil, err
	}
	if err := s.store.LockSession(ctx, claims.TenantID, claims.SessionID); err != nil {
		return ReauthResult{}, nil, err
	}
	family, err := s.store.Family(ctx, claims.TenantID, userID, claims.SessionID)
	if err != nil {
		return ReauthResult{}, nil, err
	}
	now := s.now().UTC()
	if !activeReauthFamily(family, now) {
		outcome := revokedToken()
		return ReauthResult{}, outcome, s.reauthAudit(ctx, claims.TenantID, userID, time.Time{}, outcome)
	}
	user, found, err := store.UserForReauth(ctx, claims.TenantID, userID)
	if err != nil {
		return ReauthResult{}, nil, err
	}
	matches = matches && found && candidate.ID == user.ID && candidate.PasswordHash == user.PasswordHash
	lastAt, lastIP := user.LastLoginAt, user.LastLoginIP
	outcome := s.loginDecision(&user, found, matches, RequestMeta{}, now)
	user.LastLoginAt, user.LastLoginIP = lastAt, lastIP
	if found {
		if err := s.store.SaveLogin(ctx, user); err != nil {
			return ReauthResult{}, nil, err
		}
	}
	if err := s.reauthAudit(ctx, claims.TenantID, userID, now, outcome); err != nil {
		return ReauthResult{}, nil, err
	}
	if outcome != nil {
		return ReauthResult{}, outcome, nil
	}
	if err := store.SaveReauth(ctx, claims.TenantID, userID, claims.SessionID, now); err != nil {
		return ReauthResult{}, nil, err
	}
	return ReauthResult{ReauthAt: now, ExpiresAt: now.Add(5 * time.Minute)}, nil, nil
}

func activeReauthFamily(family repo.Family, now time.Time) bool {
	if familyRevoked(family) {
		return false
	}
	for _, token := range family.Tokens {
		if token.RevokedAt == nil && token.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

func (s *AuthService) reauthAudit(ctx context.Context, tenant, user int64, now time.Time, outcome error) error {
	event := audit.Event{TenantID: tenant, ActorType: "user", ActorID: &user, Action: "identity.reauth", ResourceType: "user", ResourceID: strconv.FormatInt(user, 10), Result: "failure"}
	if outcome == nil {
		event.Result, event.After = "success", map[string]any{"reauth_at": now}
	}
	return s.audit(ctx, event)
}

// RequireRecent checks live family state and a strict five-minute reauthentication window.
func (s *AuthService) RequireRecent(ctx context.Context, tenant, user int64, sid string) error {
	if tenant <= 0 || user <= 0 || sid == "" {
		return recentRequired()
	}
	revoked, err := s.redis.Revoked(ctx, session.RevocationKey(sid))
	if err != nil {
		return dependencyFailure(err)
	}
	if revoked {
		return recentRequired()
	}
	store, ok := s.store.(repo.Reauthentication)
	if !ok {
		return dependencyFailure(errors.New("reauthentication persistence unavailable"))
	}
	now := s.now().UTC()
	at, err := store.ReauthAt(ctx, tenant, user, sid, now)
	if err != nil {
		return dependencyFailure(err)
	}
	if at != nil {
		age := now.Sub(*at)
		if age >= 0 && age < 5*time.Minute {
			return nil
		}
	}
	return recentRequired()
}

func recentRequired() error {
	return errs.New(errs.ErrDataScopeDenied, 403, "需要近期重新认证")
}

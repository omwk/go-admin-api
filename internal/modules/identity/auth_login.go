package identity

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/session"
)

// Login retains the internal legacy-ID entry point, using the resolved code for login limits.
func (s *AuthService) Login(ctx context.Context, tenant int64, username, password string, meta RequestMeta) (Tokens, error) {
	if tenant <= 0 || username == "" || password == "" {
		return Tokens{}, errs.New(errs.ErrInvalidParam, 400, "用户名和密码不能为空")
	}
	return s.loginTenant(ctx, "", tenant, username, password, meta)
}

func (s *AuthService) loginKnownTenant(ctx context.Context, tenant int64, username, password string, meta RequestMeta, policy *setting.Security) (Tokens, error) {
	candidate, candidateFound, err := s.store.UserForLogin(ctx, tenant, username)
	if err != nil {
		return Tokens{}, dependencyFailure(err)
	}
	hash := s.dummyHash
	if candidateFound {
		hash = candidate.PasswordHash
	}
	passwordMatches := s.verify(hash, password) == nil
	*policy, err = setting.LoadSecurity(ctx, s.settings, tenant)
	if err != nil {
		return Tokens{}, dependencyFailure(err)
	}
	var tokens Tokens
	var outcome error
	err = s.store.Transaction(ctx, func(txCtx context.Context) error {
		if candidateFound {
			if err := s.lockAccount(txCtx, tenant, candidate.ID); err != nil {
				return err
			}
		}
		user, found, err := s.store.UserForLogin(txCtx, tenant, username)
		if err != nil {
			return err
		}
		matches := passwordMatches && candidateFound && candidate.ID == user.ID && candidate.PasswordHash == user.PasswordHash
		if matches {
			if err := s.checkTenant(txCtx, tenant); err != nil {
				return err
			}
		}
		outcome, err = s.accountLoginDecision(txCtx, &user, found, matches, meta, *policy)
		if err != nil {
			return err
		}
		if found {
			if err := s.store.SaveLogin(txCtx, user); err != nil {
				return err
			}
		}
		if outcome == nil {
			tokens, err = s.createLogin(txCtx, user, meta)
			if err != nil {
				return err
			}
		}
		return s.loginAudit(txCtx, tenant, user, found, meta, outcome)
	})
	if err != nil {
		return Tokens{}, accountError(err)
	}
	if outcome != nil {
		return Tokens{}, outcome
	}
	if err := s.redis.Ping(ctx); err != nil {
		return Tokens{}, dependencyFailure(err)
	}
	return tokens, nil
}

func (s *AuthService) loginDecision(user *model.User, found, matches bool, meta RequestMeta, now time.Time, policies ...setting.Security) error {
	policy := setting.DefaultSecurity()
	if len(policies) > 0 {
		policy = policies[0]
	}
	if !found || user.Status == "disabled" {
		return invalidLoginCredentials()
	}
	if user.LockedUntil != nil && user.LockedUntil.After(now) {
		return lockedAccount()
	}
	if user.Status == "locked" {
		if user.LockedUntil == nil {
			return lockedAccount()
		}
		user.Status, user.FailedAttempts, user.LockedUntil = "active", 0, nil
	}
	if !matches {
		user.FailedAttempts++
		if user.FailedAttempts >= policy.LoginMaxFailures {
			until := now.Add(time.Duration(policy.LoginLockMinutes) * time.Minute)
			user.Status, user.LockedUntil = "locked", &until
			return lockedAccount()
		}
		return invalidLoginCredentials()
	}
	user.FailedAttempts, user.LockedUntil, user.LastLoginAt = 0, nil, &now
	if meta.IP != "" {
		user.LastLoginIP = &meta.IP
	}
	return nil
}

func (s *AuthService) createLogin(ctx context.Context, user model.User, meta RequestMeta) (Tokens, error) {
	sid, err := session.NewID()
	if err != nil {
		return Tokens{}, err
	}
	tokens, row, err := s.issue(user.TenantID, user.ID, sid, meta, s.now().UTC())
	if err != nil {
		return Tokens{}, err
	}
	if err := s.store.InsertRefresh(ctx, &row); err != nil {
		return Tokens{}, err
	}
	tokens.User = &UserSummary{ID: user.ID, Username: user.Username, Nickname: user.Nickname}
	tokens.PasswordStatus, err = s.passwordStatus(ctx, user, s.now().UTC())
	if err != nil {
		return Tokens{}, err
	}
	tokens.PasswordExpired = tokens.PasswordStatus == "password_expired"
	tokens.PasswordChangeRequired = tokens.PasswordStatus != ""
	return tokens, nil
}

func (s *AuthService) loginAudit(ctx context.Context, tenant int64, user model.User, found bool, meta RequestMeta, outcome error) error {
	userAgent := meta.UserAgent
	if len(userAgent) > 256 {
		userAgent = strings.ToValidUTF8(userAgent[:256], "")
	}
	event := audit.Event{TenantID: tenant, ActorType: "system", Action: "identity.login", ResourceType: "user", ResourceID: "unknown", Result: "failure",
		IP: meta.IP, UserAgent: userAgent}
	if found {
		event.ActorType, event.ActorID, event.ResourceID = "user", &user.ID, strconv.FormatInt(user.ID, 10)
	}
	if outcome == nil {
		event.Result = "success"
	}
	return s.audit(ctx, event)
}

package identity

import (
	"context"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// LoginByCode authenticates an account in the tenant selected by its exact immutable code.
func (s *AuthService) LoginByCode(ctx context.Context, code, account, password string, meta RequestMeta) (Tokens, error) {
	if code == "" || len(code) > 64 || account == "" || password == "" {
		return Tokens{}, errs.New(errs.ErrInvalidParam, 400, "登录参数无效")
	}
	return s.loginTenant(ctx, code, 0, account, password, meta)
}

func (s *AuthService) loginTenant(ctx context.Context, code string, id int64, account, password string, meta RequestMeta) (Tokens, error) {
	tenant, found, err := s.tenants.TenantForLogin(ctx, code, id)
	if err != nil {
		return Tokens{}, dependencyFailure(err)
	}
	if found {
		code = tenant.Code
	} else if code == "" {
		// This namespace cannot collide with a provisioned tenant code.
		code = "legacy-id=" + strconv.FormatInt(id, 10)
	}
	if err := s.loginLimit(ctx, code, meta); err != nil {
		if found {
			if auditErr := s.loginAudit(ctx, tenant.ID, model.User{}, false, meta, err); auditErr != nil {
				return Tokens{}, dependencyFailure(auditErr)
			}
		}
		return Tokens{}, err
	}
	accountKey := account
	if _, normalized, err := AccountIdentifier(account); err == nil {
		accountKey = normalized
	}
	policy := setting.DefaultSecurity()
	if !found {
		unknown := model.User{}
		outcome := s.loginDecision(&unknown, false, s.verify(s.dummyHash, password) == nil, meta, s.now().UTC())
		if err := s.loginResult(ctx, code, accountKey, outcome, policy); err != nil {
			return Tokens{}, err
		}
		return Tokens{}, outcome
	}
	tokens, outcome := s.loginKnownTenant(ctx, tenant.ID, account, password, meta, &policy)
	if err := s.loginResult(ctx, code, accountKey, outcome, policy); err != nil {
		return Tokens{}, err
	}
	return tokens, outcome
}

func (s *AuthService) loginLimit(ctx context.Context, code string, meta RequestMeta) error {
	allowed, err := s.redis.AllowLogin(ctx, meta.IP)
	if err == nil && allowed {
		allowed, err = s.limits.AllowTenantLogin(ctx, code)
	}
	if err != nil {
		return dependencyFailure(err)
	}
	if !allowed {
		return errs.New(errs.ErrTooManyRequests, 429, "登录请求过多")
	}
	return nil
}

func (s *AuthService) loginResult(ctx context.Context, code, account string, outcome error, policy setting.Security) error {
	if outcome != nil {
		if app := errs.From(outcome); app.Code != invalidCredentialsCode && app.Code != accountLockedCode && app.Code != errs.ErrTenantSuspended {
			return nil
		}
		if err := s.limits.RecordLoginFailure(ctx, code, account, time.Duration(policy.LoginLockMinutes)*time.Minute); err != nil {
			return dependencyFailure(err)
		}
		return nil
	}
	if err := s.limits.ClearLoginFailures(ctx, code, account); err != nil {
		return dependencyFailure(err)
	}
	return nil
}

func invalidLoginCredentials() error {
	return errs.New(invalidCredentialsCode, 401, "企业代码、账号或密码错误")
}

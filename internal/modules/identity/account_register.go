package identity

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// RegisterInput supplies identity fields only; roles and department are server controlled.
type RegisterInput struct {
	Tenant   string `json:"tenant" binding:"required,max=64"`
	Account  string `json:"account" binding:"required,max=256"`
	Password string `json:"password" binding:"required,max=72"`
	Email    string `json:"email" binding:"required,max=256"`
}

// RegistrationResult retains one data shape for creation and duplicate acceptance.
type RegistrationResult struct {
	Accepted   bool   `json:"accepted"`
	Link       string `json:"dev_verify_url,omitempty"`
	HTTPStatus int    `json:"-"`
}

// Register creates a disabled account or resends verification without replacing existing identity.
func (s *AccountService) Register(ctx context.Context, in RegisterInput, meta RequestMeta) (RegistrationResult, error) {
	store, ok := s.Store.(repo.Registration)
	if !ok {
		return RegistrationResult{}, dependencyFailure(errors.New("registration store unavailable"))
	}
	tenant, found, err := store.TenantForLogin(ctx, in.Tenant, 0)
	if err != nil {
		return RegistrationResult{}, accountError(err)
	}
	if !found {
		return RegistrationResult{}, invalidLoginCredentials()
	}
	if err := s.registrationBudget(ctx, tenant.ID); err != nil {
		return RegistrationResult{}, err
	}
	in, err = normalizeRegistration(in)
	if err != nil {
		return RegistrationResult{}, err
	}
	if err := validatePassword(ctx, s.Settings, tenant.ID, in.Password); err != nil {
		return RegistrationResult{}, err
	}
	raw, err := refresh.Generate()
	if err != nil {
		return RegistrationResult{}, accountError(err)
	}
	hash, err := pwd.Hash(in.Password)
	if err != nil {
		return RegistrationResult{}, accountError(err)
	}
	out := RegistrationResult{Accepted: true, HTTPStatus: 202}
	if s.CopyResetLinks {
		out.Link = AccountLink(s.PublicURL, "verify", tenant.ID, raw)
	}
	available, err := s.registrationMailAvailable(ctx, tenant.ID)
	if err != nil {
		return RegistrationResult{}, accountError(err)
	}
	var created bool
	err = s.Store.Transaction(ctx, func(tx context.Context) error {
		var err error
		created, err = s.registerUser(tx, store, tenant.ID, in, hash, raw, meta)
		return err
	})
	if errors.Is(err, repo.ErrRegistrationCollision) {
		return out, nil
	}
	if err != nil {
		return RegistrationResult{}, accountError(err)
	}
	if created && available {
		out.HTTPStatus = 201
	}
	return out, nil
}

func normalizeRegistration(in RegisterInput) (RegisterInput, error) {
	column, name, err := AccountIdentifier(in.Account)
	if err != nil || column == "phone" || in.Tenant == "" || len(in.Tenant) > 64 {
		return in, invalidAdmin()
	}
	emailColumn, email, err := AccountIdentifier(in.Email)
	if err != nil || emailColumn != "email" || column == "email" && name != email {
		return in, invalidAdmin()
	}
	in.Account, in.Email = name, email
	return in, nil
}

func (s *AccountService) registrationBudget(ctx context.Context, tenant int64) error {
	policy, err := setting.LoadTenantRegistration(ctx, s.Settings, tenant)
	if err != nil {
		return accountError(err)
	}
	if !policy.Allowed {
		return registrationClosed()
	}
	limits, ok := s.Limits.(repo.RegistrationLimits)
	if !ok {
		return dependencyFailure(errors.New("registration limits unavailable"))
	}
	allowed, err := limits.AllowRegistration(ctx, tenant, policy.HourlyLimit)
	if err != nil {
		return dependencyFailure(err)
	}
	if !allowed {
		return errs.New(errs.ErrTooManyRequests, 429, "请求过多，请稍后重试")
	}
	return nil
}

func (s *AccountService) registerUser(ctx context.Context, store repo.Registration, tenant int64, in RegisterInput, hash, raw string, meta RequestMeta) (bool, error) {
	if err := s.Store.LockInviteTenant(ctx, tenant); err != nil {
		return false, err
	}
	if gate, ok := s.Store.(repo.TenantGate); ok {
		if err := gate.CheckTenant(ctx, tenant); err != nil {
			return false, err
		}
	}
	policy, err := setting.LoadTenantRegistration(ctx, s.Settings, tenant)
	if err != nil {
		return false, err
	}
	if !policy.Allowed {
		return false, registrationClosed()
	}
	user, found, err := store.RegistrationUser(ctx, tenant, in.Account, in.Email)
	if err != nil {
		return false, err
	}
	now := s.Now().UTC()
	if !found {
		department, err := store.RegistrationDepartment(ctx, tenant)
		if err != nil {
			return false, err
		}
		user = model.User{TenantID: tenant, Username: in.Account, Email: &in.Email, DepartmentID: department,
			PasswordHash: hash, PasswordChangedAt: &now, Status: "disabled"}
		if err := store.CreateRegistrationUser(ctx, &user); err != nil {
			return false, err
		}
		if err := rememberInitialPassword(ctx, s, user, now); err != nil {
			return false, err
		}
	}
	if err := s.Store.LockAccount(ctx, tenant, user.ID); err != nil {
		return false, err
	}
	current, exists, err := s.Store.UserForReauth(ctx, tenant, user.ID)
	if err != nil {
		return false, err
	}
	if exists && current.Status == "disabled" && current.Email != nil && *current.Email == in.Email {
		status, ok := s.Store.(repo.RegistrationStatus)
		if !ok {
			return false, errors.New("registration status unavailable")
		}
		pending := !found
		if found {
			pending, err = status.HasPendingRegistration(ctx, tenant, current.ID)
		}
		if err != nil {
			return false, err
		}
		if pending {
			if err := s.queueVerification(ctx, store, current, raw, now, meta); err != nil {
				return false, err
			}
		}
	}
	return !found, s.registrationAudit(ctx, user, "identity.register", meta)
}

func registrationClosed() error {
	return errs.New(errs.ErrTenantSuspended, 403, "该企业未开放自助注册")
}

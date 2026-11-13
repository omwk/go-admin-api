package identity

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// AccountService implements invitations, recovery and authenticated self-service.
type AccountService struct {
	Store          repo.Account
	Auth           *AuthService
	Admin          *AdminService
	Limits         repo.AccountLimits
	Now            func() time.Time
	Notify         AccountNotifications
	CopyResetLinks bool
	PublicURL      string
	Invalidate     func(context.Context, int64, []int64) bool
	Settings       setting.Reader
}

// AccountNotifications queues safe account messages and performs delivery after commit.
type AccountNotifications interface {
	Queue(context.Context, int64, int64, string, int64, string, string) (int64, error)
	Deliver(context.Context, int64, int64) error
}

// UseAccounts enables live password restrictions and self-service routes during assembly.
func (s *AuthService) UseAccounts(accounts *AccountService) { s.accounts = accounts }

// UseAccounts applies the lifecycle password policy to administrative writes.
func (s *AdminService) UseAccounts(accounts *AccountService) { s.accounts = accounts }

func (s *AccountService) owner(ctx context.Context, claims jwt.Claims) (model.User, error) {
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || id <= 0 || claims.TenantID <= 0 {
		return model.User{}, revokedToken()
	}
	user, found, err := s.Store.UserForReauth(ctx, claims.TenantID, id)
	if err != nil {
		return user, dependencyFailure(err)
	}
	if !found || user.Status != "active" {
		return user, revokedToken()
	}
	return user, nil
}

func passwordRestriction(user model.User, now time.Time) string {
	return passwordRestrictionFor(user, now, setting.DefaultSecurity().PasswordExpireDays)
}

func passwordRequired(reason string) error {
	return &errs.AppError{Code: tokenExpiredCode, HTTPStatus: 403, Message: reason, Details: map[string]any{"reason": reason, "password_change_required": true}}
}

func invalidLink() error { return errs.New(errs.ErrInvalidParam, 400, "邀请或链接无效") }

func (s *AuthService) lockAccount(ctx context.Context, tenant, user int64) error {
	if store, ok := s.store.(repo.AccountLocker); ok {
		return store.LockAccount(ctx, tenant, user)
	}
	return nil
}

func accountError(err error) error {
	if err == nil {
		return nil
	}
	var app *errs.AppError
	if errors.As(err, &app) {
		return err
	}
	return dependencyFailure(err)
}

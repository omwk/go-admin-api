package identity

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
)

const settingsKey = "identity.settings.reader"

// UseSettings enables tenant policy reads without restarting the authentication service.
func (s *AuthService) UseSettings(reader setting.Reader) {
	s.settings = reader
	if s.accounts != nil {
		s.accounts.Settings = reader
	}
}

// UseSettings enables tenant policy reads for administrative account writes.
func (s *AdminService) UseSettings(reader setting.Reader) {
	s.settings = reader
	if s.accounts != nil {
		s.accounts.Settings = reader
	}
}

func validatePassword(ctx context.Context, reader setting.Reader, tenant int64, password string) error {
	policy, err := setting.LoadSecurity(ctx, reader, tenant)
	if err != nil {
		return dependencyFailure(err)
	}
	if pwd.ValidatePolicy(password, policy) != nil {
		return invalidPassword()
	}
	return nil
}

func (s *AuthService) passwordStatus(ctx context.Context, user model.User, now time.Time) (string, error) {
	if s.settings == nil {
		return passwordRestriction(user, now), nil
	}
	policy, err := setting.LoadSecurity(ctx, s.settings, user.TenantID)
	if err != nil {
		return "", dependencyFailure(err)
	}
	return passwordRestrictionFor(user, now, policy.PasswordExpireDays), nil
}

func passwordRestrictionFor(user model.User, now time.Time, days int) string {
	if user.ForcePasswordChange {
		return "password_change_required"
	}
	if days > 0 && user.PasswordChangedAt != nil && !user.PasswordChangedAt.Add(time.Duration(days)*24*time.Hour).After(now) {
		return "password_expired"
	}
	return ""
}

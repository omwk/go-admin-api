package repo

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// Registration keeps anonymous registration queries separate from management scope.
type Registration interface {
	LoginTenant
	RegistrationUser(context.Context, int64, string, string) (model.User, bool, error)
	RegistrationDepartment(context.Context, int64) (int64, error)
	CreateRegistrationUser(context.Context, *model.User) error
	RegistrationRoles(context.Context, int64, string) ([]int64, error)
	VerificationByHash(context.Context, string) (model.AuthToken, bool, error)
	RevokeVerificationTokens(context.Context, int64, int64, time.Time) error
	ActivateRegisteredUser(context.Context, model.User, []int64) error
}

// ErrRegistrationCollision permits an indistinguishable accepted response after a concurrent insert.
var ErrRegistrationCollision = errors.New("registration identity unavailable")

// RegistrationStatus distinguishes pending email verification from administrative disablement.
type RegistrationStatus interface {
	HasPendingRegistration(context.Context, int64, int64) (bool, error)
}

// RegistrationLimits enforces a shared tenant hourly budget, including duplicate attempts.
type RegistrationLimits interface {
	AllowRegistration(context.Context, int64, int) (bool, error)
}

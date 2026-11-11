package repo

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// AccountLocker serializes credential and session changes with concurrent login/refresh.
type AccountLocker interface {
	LockAccount(context.Context, int64, int64) error
}

// Account owns bounded identity lifecycle persistence, independent of management data scope.
type Account interface {
	AccountLocker
	Transaction(context.Context, func(context.Context) error) error
	UserForReauth(context.Context, int64, int64) (model.User, bool, error)
	UserForLogin(context.Context, int64, string) (model.User, bool, error)
	PasswordHistory(context.Context, int64, int64) ([]model.PasswordHistory, error)
	RememberPassword(context.Context, model.PasswordHistory) error
	UpdatePassword(context.Context, model.User) error
	UpdateProfile(context.Context, model.User) error
	InsertToken(context.Context, *model.AuthToken) error
	TokenByHash(context.Context, int64, string, string) (model.AuthToken, bool, error)
	TokenByID(context.Context, int64, int64) (model.AuthToken, bool, error)
	SaveToken(context.Context, model.AuthToken) error
	Invites(context.Context, int64, int64, Page) ([]model.AuthToken, int64, error)
	RevokeResetTokens(context.Context, int64, int64, time.Time) error
	CreateInvitedUser(context.Context, *model.User, []int64) error
	OwnSessions(context.Context, int64, int64, Page, time.Time) ([]Session, int64, error)
	SessionIDs(context.Context, int64, int64) ([]string, error)
}

// AccountLimits enforces independent, non-reversible IP and normalized account budgets.
type AccountLimits interface {
	AllowAccount(context.Context, string, string) (bool, error)
}

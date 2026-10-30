// Package repo defines the dedicated, bounded persistence ports for authentication.
package repo

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// Family contains persistent revocation facts and the token history of one sid.
type Family struct {
	Tokens []model.RefreshToken
}

// Auth supplies only the queries needed for login and a verified token's family.
// Implementations serialize session operations inside Transaction via LockSession.
type Auth interface {
	Transaction(context.Context, func(context.Context) error) error
	UserForLogin(context.Context, int64, string) (model.User, bool, error)
	SaveLogin(context.Context, model.User) error
	RefreshByHash(context.Context, string) (model.RefreshToken, bool, error)
	LockSession(context.Context, int64, string) error
	Family(context.Context, int64, int64, string) (Family, error)
	SessionRevoked(context.Context, int64, int64, string) (bool, error)
	InsertRefresh(context.Context, *model.RefreshToken) error
	ClaimRefresh(context.Context, model.RefreshToken, time.Time) (bool, error)
	LinkRefresh(context.Context, model.RefreshToken, int64) error
	RevokeFamily(context.Context, int64, int64, string, string, time.Time) error
}

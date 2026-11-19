package repo

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// EmailChange provides capability lookup and cancellation of pending email changes.
type EmailChange interface {
	EmailChangeByHash(context.Context, string) (model.AuthToken, bool, error)
	RevokeEmailChangeTokens(context.Context, int64, int64, time.Time) error
}

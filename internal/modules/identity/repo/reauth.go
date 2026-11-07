package repo

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// Reauthentication supplements Auth with bounded current-account and session writes.
type Reauthentication interface {
	UserForReauth(context.Context, int64, int64) (model.User, bool, error)
	SaveReauth(context.Context, int64, int64, string, time.Time) error
	ReauthAt(context.Context, int64, int64, string, time.Time) (*time.Time, error)
}

package repo

import (
	"context"
	"time"
)

// Session lists persistent, non-revoked families with an unexpired refresh, without inferred activity.
type Session struct {
	SID       string    `json:"sid"`
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Sessions is the scope-protected session management read port; auth owns locked family writes.
type Sessions interface {
	Sessions(context.Context, int64, Page, time.Time) ([]Session, int64, error)
	SessionFamily(context.Context, int64, string) (Family, error)
}

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

// OwnSession exposes only the caller's family metadata and a request-local current flag.
type OwnSession struct {
	SID       string    `json:"sid" gorm:"column:sid"`
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	IP        *string   `json:"ip"`
	UserAgent *string   `json:"user_agent"`
	Current   bool      `json:"current"`
}

// Sessions is the scope-protected session management read port; auth owns locked family writes.
type Sessions interface {
	Sessions(context.Context, int64, Page, time.Time) ([]Session, int64, error)
	SessionFamily(context.Context, int64, string) (Family, error)
}

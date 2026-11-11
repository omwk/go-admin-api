package repo

import "time"

// CredentialVersionView is the public history allowlist, excluding encrypted data.
type CredentialVersionView struct {
	ID            int64      `json:"id"`
	VersionNo     int        `json:"version_no"`
	Fingerprint   string     `json:"fingerprint"`
	IssuedAt      time.Time  `json:"issued_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	CreatedReason string     `json:"created_reason"`
	CreatedBy     int64      `json:"created_by"`
	RetiredAt     *time.Time `json:"retired_at"`
	PurgeAfter    *time.Time `json:"purge_after"`
}

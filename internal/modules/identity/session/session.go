// Package session contains session identifiers and ADR-012 revocation keys.
package session

import (
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/idgen"
)

const (
	// AccessRevocationPrefix isolates access JTI markers.
	AccessRevocationPrefix = "auth:v1:revoked:access:jti:"
	// RefreshRevocationPrefix isolates refresh SHA-256 markers.
	RefreshRevocationPrefix = "auth:v1:revoked:refresh:sha256:"
	// SessionRevocationPrefix isolates complete session-family markers.
	SessionRevocationPrefix = "auth:v1:revoked:session:"
)

// NewID creates the ULID used as the session family identifier.
func NewID() (string, error) { return idgen.NewULID() }

// AccessKey names the revocation entry for one access-token JTI.
func AccessKey(jti string) string { return AccessRevocationPrefix + jti }

// RefreshKey names the revocation entry for one refresh-token SHA-256 digest.
func RefreshKey(tokenHash string) string { return RefreshRevocationPrefix + tokenHash }

// RevocationKey names the revocation entry for a complete session family.
func RevocationKey(sessionID string) string { return SessionRevocationPrefix + sessionID }

// RemainingTTL returns zero when the referenced token or session has expired.
func RemainingTTL(expiresAt time.Time, now time.Time) time.Duration {
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

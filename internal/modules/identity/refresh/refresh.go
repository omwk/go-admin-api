// Package refresh creates opaque refresh tokens and their storage hashes.
package refresh

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

const (
	// TokenBytes is the entropy length required for each refresh token.
	TokenBytes = 32
	// TTL is the validity period required by M1 for refresh tokens.
	TTL = 7 * 24 * time.Hour
)

// Generate returns a URL-safe refresh token containing 32 random bytes.
func Generate() (string, error) {
	secret := make([]byte, TokenBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", errors.New("generate refresh token")
	}
	return base64.RawURLEncoding.EncodeToString(secret), nil
}

// Hash returns the lowercase hexadecimal SHA-256 digest stored in the database.
func Hash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// Package idgen creates identifiers used by kernel and identity records.
package idgen

import (
	"crypto/rand"
	"errors"

	"github.com/oklog/ulid/v2"
)

// NewULID returns a cryptographically random ULID with the current timestamp.
func NewULID() (string, error) {
	id, err := ulid.New(ulid.Now(), rand.Reader)
	if err != nil {
		return "", errors.New("generate identifier")
	}
	return id.String(), nil
}

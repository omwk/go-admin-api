// Package pwd hashes and verifies identity passwords.
package pwd

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const cost = 12

// Hash creates a bcrypt hash using the platform's fixed cost.
func Hash(password string) (string, error) {
	if password == "" {
		return "", errors.New("password is required")
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hashed), nil
}

// Verify compares a password with a stored bcrypt hash.
func Verify(hashed string, password string) error {
	if hashed == "" || password == "" {
		return errors.New("password hash and password are required")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(password)); err != nil {
		return errors.New("password does not match")
	}
	return nil
}

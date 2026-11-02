package connector

import "fmt"

// ErrKind is the platform-independent transport category.
type ErrKind string

const (
	ErrAuthExpired    ErrKind = "auth_expired"
	ErrRateLimited    ErrKind = "rate_limited"
	ErrUnavailable    ErrKind = "unavailable"
	ErrInvalidRequest ErrKind = "invalid_request"
)

// PlatformError contains safe retry metadata without raw upstream payloads.
type PlatformError struct {
	Kind              ErrKind
	Status            int
	RetryAfterSeconds int
	Cause             error
}

func (e *PlatformError) Error() string { return fmt.Sprintf("platform request failed: %s", e.Kind) }
func (e *PlatformError) Unwrap() error { return e.Cause }

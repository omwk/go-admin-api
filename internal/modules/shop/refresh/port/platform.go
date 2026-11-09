// Package port defines the single-dispatch platform refresh boundary.
package port

import (
	"context"
	"time"
)

// Request contains in-memory values; implementations must not log or retain them.
type Request struct {
	TenantID       int64
	CredentialID   int64
	InputVersionID int64
	Generation     int64
	Kind           string
	Values         []byte
}

// Result is a complete credential bundle with protocol-verified expiry metadata.
// The executor clears Values after sealing. No raw response metadata is persisted.
type Result struct {
	Values    []byte
	ExpiresAt time.Time
	RefreshAt time.Time
}

// Refresher performs exactly one platform call, without transport-level retries.
// A nil error means a complete replacement bundle, never an implicit merge.
type Refresher interface {
	Refresh(context.Context, Request) (Result, error)
}

// Protocol is evidence supplied by an adapter after its protocol and fixtures are verified.
type Protocol struct{ Verified, ExclusiveGrant bool }

// ProtocolRefresher gates automatic maintenance of an exclusive authorization bundle.
type ProtocolRefresher interface {
	Refresher
	RefreshProtocol() Protocol
}

// Failure carries only normalized, non-sensitive facts verified by an adapter.
// NotConsumed must be proven; a timeout or HTTP status alone cannot prove it.
type Failure struct {
	HTTPStatus  int
	Timeout     bool
	NotConsumed bool
	Revoked     bool
	RetryAfter  time.Time
}

// Error intentionally excludes all upstream data and request values.
func (*Failure) Error() string { return "platform refresh failed" }

package gateway

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
)

// Client is an application identity, independent of human sessions.
type Client struct {
	ID          int64
	TenantID    int64
	AppID       string
	Name        string
	OwnerUserID int64
	Status      string
	IPWhitelist []string `gorm:"-"`
	Version     int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Secret contains encrypted key material and is never an HTTP DTO.
type Secret struct {
	ID, TenantID, ClientID int64
	Fingerprint            string
	SecretPrefix           string
	CipherText, Nonce      []byte
	KeyVersion, Version    int
	Status                 string
	ExpiresAt              *time.Time
	CreatedAt              time.Time
	LastUsedAt             *time.Time
}

// Principal contains an identity verified against the primary database.
type Principal struct {
	TenantID, ClientID, OwnerUserID, SecretID int64
	AppID                                     string
}

// Actor names the human responsible for a management operation.
type Actor struct{ TenantID, UserID int64 }

// IssuedSecret is returned once, only by secret issuance.
type IssuedSecret struct {
	ID           int64      `json:"id"`
	Secret       string     `json:"secret"`
	SecretPrefix string     `json:"secret_prefix"`
	ExpiresAt    *time.Time `json:"expires_at"`
}

// AuthStore reads current application and key state from the primary database.
type AuthStore interface {
	FindClient(context.Context, string) (Client, error)
	Secrets(context.Context, int64, int64) ([]Secret, error)
	Touch(context.Context, Principal, time.Time) error
}

// Nonces atomically reserves an application-scoped nonce before HMAC verification.
type Nonces interface {
	Claim(context.Context, string, string, time.Duration) (bool, error)
}

// Store supplies transactional lifecycle persistence and sanitized audit writes.
type Store interface {
	AuthStore
	Transaction(context.Context, func(context.Context) error) error
	OwnerExists(context.Context, int64, int64) (bool, error)
	CreateClient(context.Context, *Client) error
	LockClient(context.Context, int64, int64) (Client, error)
	NextSecretID(context.Context) (int64, error)
	InsertSecret(context.Context, Secret) error
	ExpireSecrets(context.Context, int64, int64, time.Time) error
	SetSecretExpiry(context.Context, int64, int64, int64, time.Time) error
	RevokeSecret(context.Context, int64, int64, int64) error
	SetClientStatus(context.Context, int64, int64, int, string) error
	Audit(context.Context, audit.Event) error
}

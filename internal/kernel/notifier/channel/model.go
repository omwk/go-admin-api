package channel

import (
	"context"
	"encoding/json"
	"time"
)

// Provider is a transport configuration; Config is always an encrypted envelope.
type Provider struct {
	ID        int64           `json:"id"`
	TenantID  *int64          `json:"tenant_id"`
	Channel   string          `json:"channel"`
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"-" gorm:"type:jsonb"`
	Enabled   bool            `json:"enabled"`
	Priority  int             `json:"priority"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Template is a tenant override or a global fallback.
type Template struct {
	ID       int64  `json:"id"`
	TenantID *int64 `json:"tenant_id"`
	Code     string `json:"code"`
	Locale   string `json:"locale"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	Format   string `json:"format"`
	Enabled  bool   `json:"enabled"`
}

// Delivery exposes only a fingerprint and domain for its recipient.
type Delivery struct {
	ID              int64      `json:"id"`
	TenantID        int64      `json:"tenant_id"`
	Channel         string     `json:"channel"`
	TemplateCode    string     `json:"template_code"`
	RecipientHash   string     `json:"recipient_hash"`
	RecipientDomain string     `json:"recipient_domain"`
	ProviderID      *int64     `json:"provider_id"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	LastError       string     `json:"last_error"`
	CreatedAt       time.Time  `json:"created_at"`
	SentAt          *time.Time `json:"sent_at"`
	NextRetryAt     *time.Time `json:"next_retry_at"`
}

// Outbox retains an encrypted request referencing an existing recipient user.
type Outbox struct {
	ID           int64
	TenantID     int64
	Channel      string
	TemplateCode string
	Payload      json.RawMessage `gorm:"type:jsonb"`
	Status       string
}

// Request never contains a destination address, including during retries.
type Request struct {
	Channel      string            `json:"channel"`
	TemplateCode string            `json:"template_code"`
	Locale       string            `json:"locale"`
	UserID       int64             `json:"user_id"`
	Variables    map[string]string `json:"variables"`
	ProviderID   int64             `json:"provider_id,omitempty"`
	Test         bool              `json:"test,omitempty"`
}

// Store supplies transactional queue ownership and tenant-scoped lookups.
type Store interface {
	Transaction(context.Context, func(context.Context) error) error
	Providers(context.Context, int64, string) ([]Provider, error)
	Template(context.Context, int64, string, string) (Template, error)
	Recipient(context.Context, int64, int64, string) (string, error)
	Insert(context.Context, *Delivery, *Outbox) error
	Claim(context.Context, int64, int64, time.Time) (Delivery, Outbox, bool, error)
	Finish(context.Context, Delivery, time.Time, string, bool) error
}

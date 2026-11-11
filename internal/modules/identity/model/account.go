package model

import (
	"encoding/json"
	"time"
)

// PasswordHistory retains only the five most recent credential hashes per user.
type PasswordHistory struct {
	ID           int64 `gorm:"primaryKey;autoIncrement"`
	TenantID     int64
	UserID       int64
	PasswordHash string `json:"-"`
	CreatedAt    time.Time
}

// TableName returns the credential history table.
func (PasswordHistory) TableName() string { return "password_history" }

// AuthToken stores an opaque token digest and invitation provisioning metadata.
type AuthToken struct {
	ID           int64           `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID     int64           `json:"tenant_id"`
	UserID       *int64          `json:"user_id,omitempty"`
	Purpose      string          `json:"purpose"`
	TokenHash    string          `json:"-"`
	ExpiresAt    time.Time       `json:"expires_at"`
	UsedAt       *time.Time      `json:"used_at,omitempty"`
	Attempts     int             `json:"attempts"`
	CreatedBy    int64           `json:"created_by"`
	CreatedAt    time.Time       `json:"created_at"`
	DepartmentID *int64          `gorm:"column:dept_id" json:"dept_id,omitempty"`
	RoleIDs      json.RawMessage `gorm:"type:jsonb" json:"role_ids"`
	Username     *string         `json:"username,omitempty"`
	MaxUses      int             `json:"max_uses"`
	UsedCount    int             `json:"used_count"`
	RevokedAt    *time.Time      `json:"revoked_at,omitempty"`
}

// TableName returns the shared invitation/reset token table.
func (AuthToken) TableName() string { return "auth_token" }

// Valid checks expiry and single or bounded use semantics without disclosing the cause.
func (t AuthToken) Valid(now time.Time) bool {
	return t.RevokedAt == nil && t.UsedAt == nil && t.ExpiresAt.After(now) && (t.MaxUses == 0 || t.UsedCount < t.MaxUses)
}

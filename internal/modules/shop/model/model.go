// Package model contains shop and encrypted credential persistence models.
package model

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// Platform is a global, read-only seeded platform directory entry.
type Platform struct {
	ID           int64           `gorm:"column:id;primaryKey;autoIncrement"`
	Code         string          `gorm:"column:code;not null"`
	Name         string          `gorm:"column:name;not null"`
	AuthType     string          `gorm:"column:auth_type;not null"`
	Capabilities json.RawMessage `gorm:"column:capabilities;type:jsonb;not null;default:'{}'"`
	ConfigSchema json.RawMessage `gorm:"column:config_schema;type:jsonb;not null;default:'{}'"`
	Status       string          `gorm:"column:status;not null;default:active"`
}

// TableName returns the global platform table.
func (Platform) TableName() string { return "platform" }

// Shop is the ownership and department root; Version is its optimistic lock.
type Shop struct {
	ID             int64           `gorm:"column:id;primaryKey;autoIncrement"`
	TenantID       int64           `gorm:"column:tenant_id;not null;default:1"`
	PlatformID     int64           `gorm:"column:platform_id;not null"`
	Name           string          `gorm:"column:name;not null"`
	ExternalShopID string          `gorm:"column:external_shop_id;not null"`
	Region         string          `gorm:"column:region;not null;default:''"`
	Timezone       string          `gorm:"column:timezone;not null"`
	Currency       string          `gorm:"column:currency;type:char(3);not null"`
	OwnerUserID    int64           `gorm:"column:owner_user_id;not null"`
	DepartmentID   int64           `gorm:"column:dept_id;not null"`
	Status         string          `gorm:"column:status;not null;default:active"`
	Settings       json.RawMessage `gorm:"column:settings;type:jsonb;not null;default:'{}'"`
	LastSyncAt     *time.Time      `gorm:"column:last_sync_at"`
	Version        int             `gorm:"column:version;not null;default:0"`
	CreatedAt      time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time       `gorm:"column:updated_at;not null"`
	DeletedAt      gorm.DeletedAt  `gorm:"column:deleted_at"`
}

// TableName returns the scoped shop table.
func (Shop) TableName() string { return "shop" }

// Credential stores public metadata and a pointer to its current encrypted version.
type Credential struct {
	ID                  int64           `gorm:"column:id;primaryKey;autoIncrement"`
	TenantID            int64           `gorm:"column:tenant_id;not null;default:1"`
	ShopID              int64           `gorm:"column:shop_id;not null"`
	Kind                string          `gorm:"column:kind;not null"`
	Label               string          `gorm:"column:label;not null;default:''"`
	Status              string          `gorm:"column:status;not null;default:active"`
	CurrentVersionID    *int64          `gorm:"column:current_version_id"`
	ExpiresAt           *time.Time      `gorm:"column:expires_at"`
	RefreshAt           *time.Time      `gorm:"column:refresh_at"`
	LastCheckedAt       *time.Time      `gorm:"column:last_checked_at"`
	LastRefreshedAt     *time.Time      `gorm:"column:last_refreshed_at"`
	RefreshStrategy     string          `gorm:"column:refresh_strategy;not null;default:none"`
	ConsecutiveFailures int             `gorm:"column:consecutive_failures;not null;default:0"`
	RetryBudget         int             `gorm:"column:retry_budget;not null;default:5"`
	RefreshGeneration   int64           `gorm:"column:refresh_generation;not null;default:0"`
	Extra               json.RawMessage `gorm:"column:extra;type:jsonb;not null;default:'{}'"`
	CreatedAt           time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt           time.Time       `gorm:"column:updated_at;not null"`
}

// TableName returns the credential metadata table.
func (Credential) TableName() string { return "shop_credential" }

// CredentialVersion stores immutable versioned AEAD data; purging clears cipher and nonce.
type CredentialVersion struct {
	ID            int64           `gorm:"column:id;primaryKey;autoIncrement" json:"-"`
	TenantID      int64           `gorm:"column:tenant_id;not null;default:1" json:"-"`
	CredentialID  int64           `gorm:"column:credential_id;not null" json:"-"`
	VersionNo     int             `gorm:"column:version_no;not null" json:"-"`
	CipherText    []byte          `gorm:"column:cipher_text" json:"-"`
	Nonce         []byte          `gorm:"column:nonce" json:"-"`
	KeyVersion    int             `gorm:"column:key_version;not null" json:"-"`
	AAD           string          `gorm:"column:aad;not null" json:"-"`
	Fingerprint   string          `gorm:"column:fingerprint;not null" json:"-"`
	IssuedAt      time.Time       `gorm:"column:issued_at;not null;default:now()" json:"-"`
	ExpiresAt     *time.Time      `gorm:"column:expires_at" json:"-"`
	CreatedReason string          `gorm:"column:created_reason;not null" json:"-"`
	CreatedBy     int64           `gorm:"column:created_by;not null;default:0" json:"-"`
	RawMeta       json.RawMessage `gorm:"column:raw_meta;type:jsonb;not null;default:'{}'" json:"-"`
	RetiredAt     *time.Time      `gorm:"column:retired_at" json:"-"`
	PurgeAfter    *time.Time      `gorm:"column:purge_after" json:"-"`
}

// TableName returns the credential version table.
func (CredentialVersion) TableName() string { return "credential_version" }

// CredentialAudit records credential operations without values or ciphertext.
type CredentialAudit struct {
	ID           int64           `gorm:"column:id;primaryKey;autoIncrement"`
	TenantID     int64           `gorm:"column:tenant_id;not null;default:1"`
	CredentialID int64           `gorm:"column:credential_id;not null"`
	Action       string          `gorm:"column:action;not null"`
	ActorType    string          `gorm:"column:actor_type;not null"`
	ActorID      *int64          `gorm:"column:actor_id"`
	Result       string          `gorm:"column:result;not null"`
	ErrorCode    string          `gorm:"column:error_code;not null;default:''"`
	ErrorMessage string          `gorm:"column:error_msg;not null;default:''"`
	Detail       json.RawMessage `gorm:"column:detail;type:jsonb;not null;default:'{}'"`
	IP           *string         `gorm:"column:ip"`
	TraceID      string          `gorm:"column:trace_id;not null"`
	CreatedAt    time.Time       `gorm:"column:created_at;not null"`
}

// TableName returns the credential audit table.
func (CredentialAudit) TableName() string { return "credential_audit" }

// FileObject records metadata for an object stored outside PostgreSQL.
type FileObject struct {
	ID           int64     `gorm:"column:id;primaryKey;autoIncrement"`
	TenantID     int64     `gorm:"column:tenant_id;not null;default:1"`
	Bucket       string    `gorm:"column:bucket;not null"`
	ObjectKey    string    `gorm:"column:object_key;not null"`
	OriginalName string    `gorm:"column:original_name;not null"`
	MIME         string    `gorm:"column:mime;not null"`
	SizeBytes    int64     `gorm:"column:size_bytes;not null"`
	SHA256       string    `gorm:"column:sha256;not null"`
	Purpose      string    `gorm:"column:purpose;not null"`
	UploadedBy   *int64    `gorm:"column:uploaded_by"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
}

// TableName returns the file object table.
func (FileObject) TableName() string { return "file_object" }

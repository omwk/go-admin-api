package model

import (
	"encoding/json"
	"time"
)

// Tenant is the framework tenant directory, with globally unique immutable code.
type Tenant struct {
	ID              int64           `gorm:"primaryKey;autoIncrement" json:"id"`
	Code            string          `json:"code"`
	Name            string          `json:"name"`
	Status          string          `json:"status"`
	Settings        json.RawMessage `gorm:"type:jsonb" json:"-"`
	ProvisionedFrom *int64          `json:"provisioned_from"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// TableName returns the global directory table.
func (Tenant) TableName() string { return "tenant" }

// TenantSummary adds current non-deleted membership counts to directory listings.
type TenantSummary struct {
	Tenant
	AdminActiveCount int64 `json:"admin_active_count"`
	MemberCount      int64 `json:"member_count"`
}

// TenantMember is the complete allowlist for platform membership inspection.
type TenantMember struct {
	ID        int64     `json:"id"`
	Account   string    `gorm:"column:username" json:"account"`
	Nickname  *string   `json:"nickname"`
	Status    string    `json:"status"`
	IsSuper   bool      `json:"is_super"`
	CreatedAt time.Time `json:"created_at"`
}

// TenantTemplate retains immutable snapshots; updates only change display metadata.
type TenantTemplate struct {
	ID          int64           `gorm:"primaryKey;autoIncrement" json:"id"`
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Roles       json.RawMessage `gorm:"type:jsonb" json:"roles"`
	Menus       json.RawMessage `gorm:"type:jsonb" json:"menus"`
	Permissions json.RawMessage `gorm:"type:jsonb" json:"permissions"`
	Settings    json.RawMessage `gorm:"type:jsonb" json:"settings"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// TableName returns the framework snapshot table.
func (TenantTemplate) TableName() string { return "tenant_template" }

// TemplateRole replaces tenant-bound IDs with stable permission codes and menu keys.
type TemplateRole struct {
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	DataScope   string   `json:"data_scope"`
	Remark      *string  `json:"remark"`
	Status      string   `json:"status"`
	Permissions []string `json:"permissions"`
	Menus       []string `json:"menus"`
}

// TemplateMenu uses a snapshot-local key to preserve even absent/duplicate route paths.
type TemplateMenu struct {
	Menu
	Key       string `json:"key"`
	ParentKey string `json:"parent_key"`
}

// TenantSnapshot contains framework objects only; it has no business-data fields.
type TenantSnapshot struct {
	Roles       []TemplateRole
	Menus       []TemplateMenu
	Permissions []Permission
}

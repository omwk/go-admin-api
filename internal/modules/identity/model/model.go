// Package model contains the identity persistence models.
package model

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// Department represents a tenant's position in the department tree.
type Department struct {
	ID       int64  `gorm:"column:id;primaryKey;autoIncrement"`
	TenantID int64  `gorm:"column:tenant_id;not null;default:1"`
	ParentID int64  `gorm:"column:parent_id;not null;default:0"`
	Name     string `gorm:"column:name;not null"`
	Path     string `gorm:"column:path;not null"`
	Sort     int    `gorm:"column:sort;not null;default:0"`
	Status   string `gorm:"column:status;not null"`
}

// TableName returns the department persistence table.
func (Department) TableName() string { return "sys_dept" }

// User stores account identity, credentials and login protection state.
type User struct {
	ID             int64          `gorm:"column:id;primaryKey;autoIncrement"`
	TenantID       int64          `gorm:"column:tenant_id;not null;default:1"`
	Username       string         `gorm:"column:username;not null"`
	PasswordHash   string         `gorm:"column:password_hash;not null"`
	Nickname       *string        `gorm:"column:nickname"`
	Email          *string        `gorm:"column:email"`
	Phone          *string        `gorm:"column:phone"`
	Avatar         *string        `gorm:"column:avatar"`
	DepartmentID   int64          `gorm:"column:dept_id;not null"`
	Status         string         `gorm:"column:status;not null"`
	FailedAttempts int            `gorm:"column:failed_attempts;not null;default:0"`
	LockedUntil    *time.Time     `gorm:"column:locked_until"`
	LastLoginAt    *time.Time     `gorm:"column:last_login_at"`
	LastLoginIP    *string        `gorm:"column:last_login_ip"`
	IsSuper        bool           `gorm:"column:is_super;not null;default:false"`
	CreatedAt      time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time      `gorm:"column:updated_at;not null"`
	DeletedAt      gorm.DeletedAt `gorm:"column:deleted_at"`
}

// TableName returns the account persistence table.
func (User) TableName() string { return "sys_user" }

// Role defines a tenant role and its data scope.
type Role struct {
	ID        int64   `gorm:"column:id;primaryKey;autoIncrement"`
	TenantID  int64   `gorm:"column:tenant_id;not null;default:1"`
	Code      string  `gorm:"column:code;not null"`
	Name      string  `gorm:"column:name;not null"`
	DataScope string  `gorm:"column:data_scope;not null"`
	Remark    *string `gorm:"column:remark"`
	Status    string  `gorm:"column:status;not null"`
}

// TableName returns the role persistence table.
func (Role) TableName() string { return "sys_role" }

// Permission describes one three-part permission code.
type Permission struct {
	ID       int64  `gorm:"column:id;primaryKey;autoIncrement"`
	TenantID int64  `gorm:"column:tenant_id;not null;default:1"`
	Code     string `gorm:"column:code;not null"`
	Name     string `gorm:"column:name;not null"`
	Module   string `gorm:"column:module;not null"`
	Type     string `gorm:"column:type;not null"`
}

// TableName returns the permission persistence table.
func (Permission) TableName() string { return "sys_permission" }

// Menu stores one route or navigation item with an optional permission reference.
type Menu struct {
	ID             int64           `gorm:"column:id;primaryKey;autoIncrement"`
	TenantID       int64           `gorm:"column:tenant_id;not null;default:1"`
	ParentID       int64           `gorm:"column:parent_id;not null;default:0"`
	Name           string          `gorm:"column:name;not null"`
	Type           string          `gorm:"column:type;not null"`
	Path           *string         `gorm:"column:path"`
	Component      *string         `gorm:"column:component"`
	Redirect       *string         `gorm:"column:redirect"`
	Icon           *string         `gorm:"column:icon"`
	Sort           int             `gorm:"column:sort;not null;default:0"`
	Visible        bool            `gorm:"column:visible;not null;default:false"`
	KeepAlive      bool            `gorm:"column:keep_alive;not null;default:false"`
	IsExternal     bool            `gorm:"column:is_external;not null;default:false"`
	PermissionCode *string         `gorm:"column:permission_code"`
	Meta           json.RawMessage `gorm:"column:meta;type:jsonb;not null;default:'{}'"`
	Status         string          `gorm:"column:status;not null"`
}

// TableName returns the menu persistence table.
func (Menu) TableName() string { return "sys_menu" }

// UserRole links an account to a role in the same tenant.
type UserRole struct {
	TenantID int64 `gorm:"column:tenant_id;not null;default:1"`
	UserID   int64 `gorm:"column:user_id;primaryKey"`
	RoleID   int64 `gorm:"column:role_id;primaryKey"`
}

// TableName returns the user-role link table.
func (UserRole) TableName() string { return "sys_user_role" }

// RoleMenu links a role to a menu in the same tenant.
type RoleMenu struct {
	TenantID int64 `gorm:"column:tenant_id;not null;default:1"`
	RoleID   int64 `gorm:"column:role_id;primaryKey"`
	MenuID   int64 `gorm:"column:menu_id;primaryKey"`
}

// TableName returns the role-menu link table.
func (RoleMenu) TableName() string { return "sys_role_menu" }

// RolePermission links a role to an authorization capability in the same tenant.
type RolePermission struct {
	TenantID     int64 `gorm:"column:tenant_id;not null;default:1"`
	RoleID       int64 `gorm:"column:role_id;primaryKey"`
	PermissionID int64 `gorm:"column:permission_id;primaryKey"`
}

// TableName returns the role-permission link table.
func (RolePermission) TableName() string { return "sys_role_permission" }

// RoleDepartment lists the departments included in a custom data scope.
type RoleDepartment struct {
	TenantID int64 `gorm:"column:tenant_id;not null;default:1"`
	RoleID   int64 `gorm:"column:role_id;primaryKey"`
	DeptID   int64 `gorm:"column:dept_id;primaryKey"`
}

// TableName returns the role-department link table.
func (RoleDepartment) TableName() string { return "sys_role_dept" }

// RefreshToken stores a digest and the persistent history of one session-chain token.
type RefreshToken struct {
	ID                int64      `gorm:"column:id;primaryKey;autoIncrement"`
	TenantID          int64      `gorm:"column:tenant_id;not null;default:1"`
	UserID            int64      `gorm:"column:user_id;not null"`
	SessionID         string     `gorm:"column:session_id;not null"`
	TokenHash         string     `gorm:"column:token_hash;not null"`
	ReplacedByTokenID *int64     `gorm:"column:replaced_by_token_id"`
	RevokedReason     *string    `gorm:"column:revoked_reason"`
	ExpiresAt         time.Time  `gorm:"column:expires_at;not null"`
	RevokedAt         *time.Time `gorm:"column:revoked_at"`
	IP                *string    `gorm:"column:ip"`
	UserAgent         string     `gorm:"column:user_agent;not null;default:''"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null"`
}

// TableName returns the refresh-token history table.
func (RefreshToken) TableName() string { return "sys_refresh_token" }

package identity

import (
	"encoding/json"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// UserFields are the only scalar fields accepted by a user update.
type UserFields struct {
	Nickname     *string `json:"nickname" binding:"omitempty,max=256"`
	Email        *string `json:"email" binding:"omitempty,email,max=256"`
	Avatar       *string `json:"avatar" binding:"omitempty,max=2048"`
	DepartmentID int64   `json:"dept_id" binding:"required,gt=0"`
	Status       string  `json:"status" binding:"required,oneof=active disabled locked"`
}

// CreateUserRequest accepts a username and password only at account creation.
type CreateUserRequest struct {
	UserFields
	Username string `json:"username" binding:"required,max=256"`
	Password string `json:"password" binding:"required,min=8,max=72"`
}

// AdminUser is the safe management projection, excluding credentials and login protection.
type AdminUser struct {
	ID       int64  `json:"id"`
	TenantID int64  `json:"tenant_id"`
	Username string `json:"username"`
	UserFields
	IsSuper   bool      `json:"is_super"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func adminUser(row model.User) AdminUser {
	return AdminUser{ID: row.ID, TenantID: row.TenantID, Username: row.Username,
		UserFields: UserFields{Nickname: row.Nickname, Email: row.Email, Avatar: row.Avatar, DepartmentID: row.DepartmentID, Status: row.Status},
		IsSuper:    row.IsSuper, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC()}
}

// RoleRequest contains scalar role configuration; grants use dedicated endpoints.
type RoleRequest struct {
	Code      string  `json:"code" binding:"required,max=64"`
	Name      string  `json:"name" binding:"required,max=256"`
	DataScope string  `json:"data_scope" binding:"required,oneof=all dept dept_and_child self custom"`
	Remark    *string `json:"remark" binding:"omitempty,max=2048"`
	Status    string  `json:"status" binding:"required,oneof=active disabled"`
}

// MenuRequest contains all editable route and presentation fields, including parent_id.
type MenuRequest struct {
	ParentID       int64           `json:"parent_id" binding:"gte=0"`
	Name           string          `json:"name" binding:"required,max=256"`
	Type           string          `json:"type" binding:"required,oneof=dir menu button link"`
	Path           *string         `json:"path" binding:"omitempty,max=2048"`
	Component      *string         `json:"component" binding:"omitempty,max=2048"`
	Redirect       *string         `json:"redirect" binding:"omitempty,max=2048"`
	Icon           *string         `json:"icon" binding:"omitempty,max=256"`
	Sort           int             `json:"sort"`
	Visible        bool            `json:"visible"`
	KeepAlive      bool            `json:"keep_alive"`
	IsExternal     bool            `json:"is_external"`
	PermissionCode *string         `json:"permission_code"`
	Meta           json.RawMessage `json:"meta,omitempty"`
	Status         string          `json:"status" binding:"required,oneof=active disabled"`
}

func (r MenuRequest) row(tenant, id int64) model.Menu {
	meta := r.Meta
	if len(meta) == 0 {
		meta = json.RawMessage("{}")
	}
	return model.Menu{ID: id, TenantID: tenant, ParentID: r.ParentID, Name: r.Name, Type: r.Type, Path: r.Path,
		Component: r.Component, Redirect: r.Redirect, Icon: r.Icon, Sort: r.Sort, Visible: r.Visible,
		KeepAlive: r.KeepAlive, IsExternal: r.IsExternal, PermissionCode: r.PermissionCode, Meta: meta, Status: r.Status}
}

// AdminMenu includes hidden/disabled nodes and buttons for configuration, unlike /auth/menus.
type AdminMenu struct {
	model.Menu
	Children []AdminMenu `json:"children"`
}

// AdminPage is the standard paginated response.
type AdminPage[T any] struct {
	List     []T   `json:"list"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

package repo

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// IdentityRow is a safe user/role/permission projection; it cannot hold a password hash.
type IdentityRow struct {
	ID               int64
	TenantID         int64
	TenantCode       string
	Username         string
	Nickname         *string
	Avatar           *string
	DepartmentID     int64
	IsSuper          bool
	RoleID           *int64
	RoleCode         *string
	RoleName         *string
	PermissionCode   *string
	PermissionModule *string
}

// Profile supplies verified-identity reads and the two bounded menu queries.
type Profile interface {
	Identity(context.Context, int64, int64) ([]IdentityRow, error)
	Menus(context.Context, int64) ([]model.Menu, error)
	RoleMenus(context.Context, int64, int64) ([]int64, error)
}

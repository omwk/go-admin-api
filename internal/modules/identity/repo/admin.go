package repo

import (
	"context"
	"encoding/json"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// LinkKind selects a fixed, internal authorization relationship; never a client table name.
type LinkKind string

// Supported authorization relationships.
const (
	UserRoles       LinkKind = "user_roles"
	RoleMenus       LinkKind = "role_menus"
	RolePermissions LinkKind = "role_permissions"
	RoleDepartments LinkKind = "role_departments"
)

// Page is a validated pagination/sort request with optional user filters.
type Page struct {
	Number, Size int
	Sort         string
	Keyword      string
	Status       string
}

// SavedResponse is a committed, safe idempotency result, without credentials.
type SavedResponse struct {
	Status              int
	Data                json.RawMessage
	InvalidationPending bool
}

// AuthzChange identifies one internal management mutation's authorization impact.
type AuthzChange struct {
	Resource string
	ID       int64
}

// GrantPolicy describes the operator's current grantable roles, menus and departments.
type GrantPolicy struct {
	Roles               []model.Role
	MenuIDs             []int64
	Departments         []model.Department
	CustomDepartmentIDs []int64
}

// Admin is the management persistence port. Every resource method is tenant bound.
// Authorization is loaded by the service from a verified identity before invoking this port.
type Admin interface {
	AdminReader
	AffectedUsers(context.Context, int64, AuthzChange) ([]int64, error)
	Authorize(context.Context, int64, int64) (context.Context, error)
	Transaction(context.Context, func(context.Context) error) error
	LockManagement(context.Context, int64) error
	Users(context.Context, int64, Page) ([]model.User, int64, error)
	User(context.Context, int64, int64) (model.User, error)
	CreateUser(context.Context, *model.User) error
	UpdateUser(context.Context, int64, int64, map[string]any) error
	DeleteUser(context.Context, int64, int64) error
	Roles(context.Context, int64, Page) ([]model.Role, int64, error)
	Role(context.Context, int64, int64) (model.Role, error)
	CreateRole(context.Context, *model.Role) error
	UpdateRole(context.Context, int64, int64, map[string]any) error
	DeleteRole(context.Context, int64, int64) error
	Menus(context.Context, int64) ([]model.Menu, error)
	Menu(context.Context, int64, int64) (model.Menu, error)
	CreateMenu(context.Context, *model.Menu) error
	UpdateMenu(context.Context, int64, int64, map[string]any) error
	DeleteMenu(context.Context, int64, int64) error
	Permissions(context.Context, int64) ([]model.Permission, error)
	References(context.Context, int64, string, int64) (map[string]int64, error)
	GrantPolicy(context.Context, int64, int64) (GrantPolicy, error)
	Links(context.Context, int64, LinkKind, int64) ([]int64, error)
	ReplaceLinks(context.Context, int64, LinkKind, int64, []int64) error
	Replay(context.Context, int64, string, string, string) (SavedResponse, bool, error)
	SaveResponse(context.Context, int64, string, string, string, SavedResponse) error
}

type AuthorizationInvalidationEmitter interface {
	EmitAuthorizationInvalidation(context.Context, int64, []int64) error
}

// Package scope declares identity query ownership and trusted effective data access.
package scope

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// Declaration fixes the protected root and its ownership mapping; clients cannot supply it.
type Declaration struct {
	Entity, Alias, TenantColumn, OwnerColumn, DepartmentSource string
	Shared                                                     bool
}

// Lookup fails closed for entities absent from the internal dictionary.
func Lookup(entity string) (Declaration, error) {
	declarations := map[string]Declaration{
		"sys_user":          {"sys_user", "sys_user", "tenant_id", "id", "sys_user.dept_id", false},
		"sys_refresh_token": {"sys_refresh_token", "sys_refresh_token", "tenant_id", "user_id", "sys_user.dept_id via tenant_id/user_id", false},
		"sys_role":          {"sys_role", "sys_role", "tenant_id", "", "tenant shared + RequirePerm", true},
		"sys_menu":          {"sys_menu", "sys_menu", "tenant_id", "", "tenant shared + RequirePerm", true},
		"sys_permission":    {"sys_permission", "sys_permission", "tenant_id", "", "tenant shared + RequirePerm", true},
	}
	d, ok := declarations[entity]
	if !ok {
		return Declaration{}, Denied()
	}
	return d, nil
}

// Role is one active role's grant; an empty custom set deliberately grants no rows.
type Role struct {
	Kind        string
	Departments []int64
}

// Access is built from a verified, active account and its current effective roles.
type Access struct {
	Tenant, User, Department int64
	Path                     string
	Roles                    []Role
}

type accessKey struct{}

// WithAccess is used only by the trusted composition boundary after DB policy loading.
func WithAccess(ctx context.Context, access Access) context.Context {
	return context.WithValue(ctx, accessKey{}, access)
}

// From rejects missing identity, no effective roles and corrupt scope names.
func From(ctx context.Context) (Access, error) {
	a, ok := ctx.Value(accessKey{}).(Access)
	if !ok || a.Tenant <= 0 || a.User <= 0 || len(a.Roles) == 0 {
		return Access{}, Denied()
	}
	for _, role := range a.Roles {
		switch role.Kind {
		case "all", "dept", "dept_and_child", "self", "custom":
		default:
			return Access{}, Denied()
		}
	}
	return a, nil
}

// Denied is the existing data-scope error, independent from RBAC denial.
func Denied() error { return errs.New(20108, 403, "超出数据权限") }

// Package scope defines neutral query declarations and verified data access facts.
package scope

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// Role is one active role's grant; an empty custom set grants no rows.
type Role struct {
	Kind        string
	Departments []int64
}

// Access contains facts loaded from a verified account and its effective roles.
type Access struct {
	Tenant, User, Department int64
	Path                     string
	Roles                    []Role
}

type accessKey struct{}

// WithAccess attaches facts at the trusted composition boundary.
func WithAccess(ctx context.Context, access Access) context.Context {
	return context.WithValue(ctx, accessKey{}, access)
}

// From rejects missing identity, absent effective roles and unknown scope names.
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

// Denied preserves the existing identity data-scope failure.
func Denied() error { return errs.New(20108, 403, "超出数据权限") }

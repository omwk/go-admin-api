// Package scope exports identity's static declarations and compatibility access API.
package scope

import (
	"context"
	"sync"

	kernelscope "github.com/omwk/go-admin-api/internal/kernel/scope"
)

// Declaration is the neutral protected entity protocol.
type Declaration = kernelscope.Declaration

// QueryDeclaration is the neutral repository method protocol.
type QueryDeclaration = kernelscope.QueryDeclaration

// Role is one verified effective role's scope.
type Role = kernelscope.Role

// Access contains verified account and role facts.
type Access = kernelscope.Access

// WithAccess preserves identity's trusted composition entry point.
func WithAccess(ctx context.Context, access Access) context.Context {
	return kernelscope.WithAccess(ctx, access)
}

// From rejects absent or invalid verified access facts.
func From(ctx context.Context) (Access, error) { return kernelscope.From(ctx) }

// Denied preserves the existing data-scope error.
func Denied() error { return kernelscope.Denied() }

var registry = sync.OnceValues(func() (*kernelscope.Registry, error) {
	return kernelscope.NewRegistry(Declarations())
})

// Lookup preserves the original identity protected-root dictionary.
func Lookup(entity string) (Declaration, error) {
	switch entity {
	case "sys_user", "sys_refresh_token", "sys_role", "sys_menu", "sys_permission", "sys_dept":
		r, err := registry()
		if err != nil {
			return Declaration{}, err
		}
		return r.Lookup(entity)
	default:
		return Declaration{}, Denied()
	}
}

// Declarations returns identity's static entity and method declarations.
func Declarations() kernelscope.Set {
	entities := []Declaration{
		{Entity: "sys_user", Alias: "sys_user", TenantColumn: "tenant_id", OwnerColumn: "id", DepartmentSource: "sys_user.dept_id", DepartmentColumn: "dept_id", DeletedColumn: "deleted_at"},
		{Entity: "sys_refresh_token", Alias: "sys_refresh_token", TenantColumn: "tenant_id", OwnerColumn: "user_id", DepartmentSource: "sys_user.dept_id via tenant_id/user_id", Parent: &kernelscope.Relation{Entity: "sys_user", Alias: "scope_user", Column: "user_id", TargetColumn: "id"}},
	}
	for _, entity := range []string{"sys_role", "sys_menu", "sys_permission", "sys_dept", "sys_role_dept", "sys_role_menu", "sys_role_permission", "idempotency_key"} {
		entities = append(entities, Declaration{Entity: entity, Alias: entity, TenantColumn: "tenant_id", DepartmentSource: "tenant shared + RequirePerm or verified actor operation", Shared: true})
	}
	entities = append(entities, Declaration{Entity: "sys_user_role", Alias: "sys_user_role", TenantColumn: "tenant_id", OwnerColumn: "user_id", DepartmentSource: "sys_user.dept_id through scope-protected parent", Parent: &kernelscope.Relation{Entity: "sys_user", Alias: "scope_user", Column: "user_id", TargetColumn: "id"}})
	return kernelscope.Set{Entities: entities, Queries: queryDeclarations(entities)}
}

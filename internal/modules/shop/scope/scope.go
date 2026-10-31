// Package scope exports shop's static entity declarations, independent of identity.
package scope

import kernelscope "github.com/omwk/go-admin-api/internal/kernel/scope"

// Declarations fixes shop ownership and parent inheritance before CRUD ports exist.
func Declarations() kernelscope.Set {
	return kernelscope.Set{Entities: []kernelscope.Declaration{
		{Entity: "platform", Alias: "platform", DepartmentSource: "global read-only authenticated directory", Global: true},
		{Entity: "shop", Alias: "shop", TenantColumn: "tenant_id", OwnerColumn: "owner_user_id", DepartmentSource: "shop.dept_id", DepartmentColumn: "dept_id", DeletedColumn: "deleted_at"},
		{Entity: "shop_credential", Alias: "shop_credential", TenantColumn: "tenant_id", DepartmentSource: "protected parent shop", Parent: &kernelscope.Relation{Entity: "shop", Alias: "scope_shop", Column: "shop_id", TargetColumn: "id"}},
		{Entity: "credential_version", Alias: "credential_version", TenantColumn: "tenant_id", DepartmentSource: "protected credential to shop", Parent: &kernelscope.Relation{Entity: "shop_credential", Alias: "scope_credential", Column: "credential_id", TargetColumn: "id"}},
		{Entity: "credential_audit", Alias: "credential_audit", TenantColumn: "tenant_id", DepartmentSource: "protected credential to shop", Parent: &kernelscope.Relation{Entity: "shop_credential", Alias: "scope_credential", Column: "credential_id", TargetColumn: "id"}},
	}}
}

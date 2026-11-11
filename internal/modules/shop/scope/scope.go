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
	}, Queries: map[string]kernelscope.QueryDeclaration{
		"Shop.Platforms":                   {Roots: []kernelscope.Declaration{{Entity: "platform", Alias: "platform"}}, Policy: "authenticated global read-only directory"},
		"Shop.Shops":                       {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "verified identity + effective role union"},
		"Shop.Shop":                        {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "verified identity + effective role union"},
		"Shop.CreateShop":                  {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "verified identity + assignable owner and department"},
		"Shop.ValidateAssignment":          {Policy: "verified identity + assignable owner and department"},
		"Shop.UpdateShop":                  {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "verified identity + old and new assignment"},
		"Shop.DeleteShop":                  {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "verified identity + effective role union"},
		"Credential.CredentialTransaction": {Policy: "service-owned transaction; authorize before opening"},
		"Credential.CreateCredential":      {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "live scoped parent; shop then credential locks"},
		"Credential.Credentials":           {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "live scoped parent; metadata read audit"},
		"Credential.Credential":            {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "live scoped parent; metadata read audit"},
		"Credential.RotateCredential":      {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "live scoped parent; shop then credential locks; compare observed version"},
		"Credential.DisableCredential":     {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "live scoped parent; shop then credential locks"},
		"Credential.CheckCredential":       {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "live scoped parent; unsupported failure audit"},
		"Credential.CredentialAudits":      {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "live scoped parent; metadata read audit"},
		"Credential.CredentialVersions":    {Roots: []kernelscope.Declaration{{Entity: "shop", Alias: "shop"}}, Policy: "live scoped parent; retired version metadata only"},
	}}
}

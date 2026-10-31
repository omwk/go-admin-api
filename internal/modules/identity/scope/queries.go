package scope

import "strings"

// QueryDeclaration identifies one repository method's roots and dedicated boundary policy.
type QueryDeclaration struct {
	Roots  []Declaration
	Policy string
}

// Query refuses unregistered methods; shared configuration and dedicated auth/seed paths are explicit.
func Query(name string) (QueryDeclaration, error) {
	entities, ok := queryEntities()[name]
	if !ok {
		return QueryDeclaration{}, Denied()
	}
	policy := "verified identity + effective role union; shared configuration requires route permission"
	if strings.HasPrefix(name, "Auth.") {
		policy = "dedicated verified tenant/account, refresh digest or locked session family"
	}
	if strings.HasPrefix(name, "Profile.") {
		policy = "dedicated current verified tenant/user identity and navigation"
	}
	if strings.HasPrefix(name, "Seed.") {
		policy = "internal seed tenant; no HTTP bypass"
	}
	result := QueryDeclaration{Policy: policy}
	for _, entity := range strings.Fields(entities) {
		d, err := Lookup(entity)
		if err != nil {
			d, err = internalRelation(entity)
			if err != nil {
				return QueryDeclaration{}, err
			}
		}
		d.Alias = queryAlias(name, entity, d.Alias)
		result.Roots = append(result.Roots, d)
	}
	return result, nil
}

func queryAlias(name, entity, fallback string) string {
	if name == "Profile.Identity" {
		return "u"
	}
	if name == "Profile.RoleMenus" {
		return "rm"
	}
	if name == "Admin.Authorize" || name == "Admin.GrantPolicy" {
		switch entity {
		case "sys_role":
			return "r"
		case "sys_role_menu":
			return "rm"
		}
	}
	return fallback
}

func internalRelation(entity string) (Declaration, error) {
	switch entity {
	case "sys_dept", "sys_role_dept", "sys_role_menu", "sys_role_permission", "idempotency_key":
		return Declaration{Entity: entity, Alias: entity, TenantColumn: "tenant_id", DepartmentSource: "explicit tenant shared configuration or verified actor operation", Shared: true}, nil
	case "sys_user_role":
		return Declaration{Entity: entity, Alias: entity, TenantColumn: "tenant_id", OwnerColumn: "user_id", DepartmentSource: "sys_user.dept_id through scope-protected parent"}, nil
	default:
		return Declaration{}, Denied()
	}
}

// QueryNames returns only one port's registered method names for exact reflection comparison.
func QueryNames(port string) []string {
	result := []string{}
	for name := range queryEntities() {
		if method, ok := strings.CutPrefix(name, port+"."); ok {
			result = append(result, method)
		}
	}
	return result
}

func queryEntities() map[string]string {
	return map[string]string{
		"Admin.Transaction": "", "Admin.Authorize": "sys_user sys_role sys_dept sys_role_dept sys_role_menu", "Admin.LockManagement": "",
		"Admin.Users": "sys_user", "Admin.User": "sys_user", "Admin.CreateUser": "sys_user sys_dept", "Admin.UpdateUser": "sys_user sys_dept", "Admin.DeleteUser": "sys_user",
		"Admin.Roles": "sys_role", "Admin.Role": "sys_role", "Admin.CreateRole": "sys_role", "Admin.UpdateRole": "sys_role", "Admin.DeleteRole": "sys_role",
		"Admin.Menus": "sys_menu", "Admin.Menu": "sys_menu", "Admin.CreateMenu": "sys_menu", "Admin.UpdateMenu": "sys_menu", "Admin.DeleteMenu": "sys_menu", "Admin.Permissions": "sys_permission",
		"Admin.References":   "sys_role sys_menu sys_user_role sys_role_menu sys_role_permission sys_role_dept",
		"Admin.GrantPolicy":  "sys_role sys_dept sys_role_dept sys_role_menu",
		"Admin.Links":        "sys_user sys_user_role sys_role_menu sys_role_permission sys_role_dept",
		"Admin.ReplaceLinks": "sys_user sys_role sys_menu sys_permission sys_dept sys_user_role sys_role_menu sys_role_permission sys_role_dept",
		"Admin.Replay":       "idempotency_key", "Admin.SaveResponse": "idempotency_key", "Admin.AffectedUsers": "sys_user sys_user_role",
		"Sessions.Sessions": "sys_refresh_token", "Sessions.SessionFamily": "sys_refresh_token",
		"Auth.Transaction": "", "Auth.UserForLogin": "sys_user", "Auth.SaveLogin": "sys_user", "Auth.RefreshByHash": "sys_refresh_token", "Auth.LockSession": "sys_refresh_token", "Auth.Family": "sys_refresh_token", "Auth.SessionRevoked": "sys_refresh_token", "Auth.InsertRefresh": "sys_refresh_token", "Auth.ClaimRefresh": "sys_refresh_token", "Auth.LinkRefresh": "sys_refresh_token", "Auth.RevokeFamily": "sys_refresh_token",
		"Profile.Identity": "sys_user", "Profile.Menus": "sys_menu", "Profile.RoleMenus": "sys_role_menu",
		"Seed.Transaction": "", "Seed.LockSeed": "", "Seed.SeedPermissions": "sys_permission", "Seed.SeedMenus": "sys_menu", "Seed.SeedRoles": "sys_role sys_role_permission sys_role_menu", "Seed.SeedAdmin": "sys_user sys_dept sys_user_role",
	}
}

// Allows binds an adapter's fixed model to its method declaration.
func Allows(method, entity string) bool {
	query, err := Query(method)
	if err != nil {
		return false
	}
	for _, root := range query.Roots {
		if root.Entity == entity {
			return true
		}
	}
	return false
}

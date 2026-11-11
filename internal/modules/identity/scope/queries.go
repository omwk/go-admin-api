package scope

import "strings"

// Query refuses unregistered methods through the neutral frozen registry.
func Query(name string) (QueryDeclaration, error) {
	r, err := registry()
	if err != nil {
		return QueryDeclaration{}, err
	}
	return r.Query(name)
}

// QueryNames returns one port's registered method names for reflection comparison.
func QueryNames(port string) []string {
	r, err := registry()
	if err != nil {
		return nil
	}
	return r.QueryNames(port)
}

// Allows binds an adapter's fixed model to its method declaration.
func Allows(method, entity string) bool {
	r, err := registry()
	return err == nil && r.Allows(method, entity)
}

func queryDeclarations(entities []Declaration) map[string]QueryDeclaration {
	byName := map[string]Declaration{}
	for _, d := range entities {
		byName[d.Entity] = d
	}
	result := map[string]QueryDeclaration{}
	for name, roots := range queryEntities() {
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
		q := QueryDeclaration{Policy: policy}
		for _, entity := range strings.Fields(roots) {
			d := byName[entity]
			d.Alias = queryAlias(name, entity, d.Alias)
			q.Roots = append(q.Roots, d)
		}
		result[name] = q
	}
	return result
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

func queryEntities() map[string]string {
	return map[string]string{
		"Admin.ReadUser": "sys_user", "Admin.ReadRole": "sys_role", "Admin.ReadMenu": "sys_menu",
		"Admin.ReadLinks":   "sys_user sys_role sys_user_role sys_role_menu sys_role_permission sys_role_dept",
		"Admin.Departments": "sys_dept sys_user",
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

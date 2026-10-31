package identity

import (
	"context"
	"slices"
	"strings"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

func hasAll(policy repo.GrantPolicy) bool {
	for _, role := range policy.Roles {
		if role.DataScope == "all" {
			return true
		}
	}
	return false
}

func (s *AdminService) checkScope(ctx context.Context, actor Overview, scope string) error {
	policy, err := s.store.GrantPolicy(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return err
	}
	if len(policy.Roles) == 0 {
		return denied()
	}
	if hasAll(policy) || scope == "self" || scope == "custom" {
		return nil
	}
	for _, role := range policy.Roles {
		if role.DataScope == scope || (role.DataScope == "dept_and_child" && scope == "dept") {
			return nil
		}
	}
	return denied()
}

func allowedDepartments(actor Overview, policy repo.GrantPolicy) map[int64]bool {
	allowed := map[int64]bool{}
	path := ""
	for _, dept := range policy.Departments {
		if dept.ID == actor.User.DepartmentID {
			path = dept.Path
		}
	}
	for _, role := range policy.Roles {
		for _, dept := range policy.Departments {
			if departmentAllowed(role.DataScope, dept, actor.User.DepartmentID, path, policy.CustomDepartmentIDs) {
				allowed[dept.ID] = true
			}
		}
	}
	return allowed
}

func departmentAllowed(scope string, dept model.Department, own int64, path string, custom []int64) bool {
	switch scope {
	case "all":
		return true
	case "dept":
		return dept.ID == own
	case "dept_and_child":
		return path != "" && (dept.Path == path || strings.HasPrefix(dept.Path, path+"."))
	case "custom":
		return slices.Contains(custom, dept.ID)
	default:
		return false
	}
}

func (s *AdminService) checkDepartments(ctx context.Context, actor Overview, ids []int64) error {
	policy, err := s.store.GrantPolicy(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return err
	}
	allowed, exists := allowedDepartments(actor, policy), map[int64]bool{}
	for _, dept := range policy.Departments {
		exists[dept.ID] = true
	}
	for _, id := range ids {
		if !exists[id] {
			return adminNotFound()
		}
		if !allowed[id] {
			return denied()
		}
	}
	if len(policy.Roles) == 0 {
		return denied()
	}
	return nil
}

func (s *AdminService) canManageRole(ctx context.Context, actor Overview, role model.Role) error {
	if role.Code == SuperAdminRoleCode && !actor.User.IsSuper {
		return denied()
	}
	if err := s.checkScope(ctx, actor, role.DataScope); err != nil {
		return err
	}
	if role.DataScope == "custom" {
		depts, err := s.store.Links(ctx, actor.User.TenantID, repo.RoleDepartments, role.ID)
		if err != nil {
			return err
		}
		if err := s.checkDepartments(ctx, actor, depts); err != nil {
			return err
		}
	}
	ids, err := s.store.Links(ctx, actor.User.TenantID, repo.RolePermissions, role.ID)
	if err != nil {
		return err
	}
	return s.checkPermissionIDs(ctx, actor, ids)
}

func (s *AdminService) checkPermissionIDs(ctx context.Context, actor Overview, ids []int64) error {
	rows, err := s.store.Permissions(ctx, actor.User.TenantID)
	if err != nil {
		return err
	}
	byID := map[int64]string{}
	for _, row := range rows {
		byID[row.ID] = row.Code
	}
	for _, id := range ids {
		code, exists := byID[id]
		if !exists {
			return adminNotFound()
		}
		if !actor.User.IsSuper && !slices.Contains(actor.Permissions, code) {
			return denied()
		}
	}
	return nil
}

// Grant replaces one association through its dedicated endpoint after validating both sides.
func (s *AdminService) Grant(ctx context.Context, actor Overview, kind repo.LinkKind, id int64, ids []int64) error {
	if !validIDs(ids) {
		return invalidAdmin()
	}
	action, resource := permission.SystemRoleGrant, "role"
	if kind == repo.UserRoles {
		user, err := s.store.User(ctx, actor.User.TenantID, id)
		if err != nil {
			return err
		}
		if user.IsSuper && !actor.User.IsSuper {
			return denied()
		}
		if err := s.checkDepartments(ctx, actor, []int64{user.DepartmentID}); err != nil {
			return err
		}
		action, resource = permission.SystemUserGrant, "user"
	} else {
		role, err := s.store.Role(ctx, actor.User.TenantID, id)
		if err != nil {
			return err
		}
		if err := s.canManageRole(ctx, actor, role); err != nil {
			return err
		}
	}
	before, err := s.store.Links(ctx, actor.User.TenantID, kind, id)
	if err != nil {
		return err
	}
	if err := s.checkGrantTargets(ctx, actor, kind, append(append([]int64{}, before...), ids...)); err != nil {
		return err
	}
	if err := s.store.ReplaceLinks(ctx, actor.User.TenantID, kind, id, ids); err != nil {
		return err
	}
	return s.record(ctx, actor, action, resource, id, map[string]any{string(kind): before}, map[string]any{string(kind): ids}, false)
}

func (s *AdminService) checkGrantTargets(ctx context.Context, actor Overview, kind repo.LinkKind, ids []int64) error {
	switch kind {
	case repo.UserRoles:
		return s.checkRoleIDs(ctx, actor, ids)
	case repo.RolePermissions:
		return s.checkPermissionIDs(ctx, actor, ids)
	case repo.RoleDepartments:
		return s.checkDepartments(ctx, actor, ids)
	case repo.RoleMenus:
		return s.checkMenuIDs(ctx, actor, ids)
	default:
		return invalidAdmin()
	}
}

func (s *AdminService) checkRoleIDs(ctx context.Context, actor Overview, ids []int64) error {
	for _, id := range ids {
		role, err := s.store.Role(ctx, actor.User.TenantID, id)
		if err != nil {
			return err
		}
		if err := s.canManageRole(ctx, actor, role); err != nil {
			return err
		}
	}
	return nil
}

func (s *AdminService) checkMenuIDs(ctx context.Context, actor Overview, ids []int64) error {
	rows, err := s.store.Menus(ctx, actor.User.TenantID)
	if err != nil {
		return err
	}
	policy, err := s.store.GrantPolicy(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return err
	}
	exists := map[int64]bool{}
	for _, row := range rows {
		exists[row.ID] = true
	}
	for _, id := range ids {
		if !exists[id] {
			return adminNotFound()
		}
		if !actor.User.IsSuper && !slices.Contains(policy.MenuIDs, id) {
			return denied()
		}
	}
	return nil
}

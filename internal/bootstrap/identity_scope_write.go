package bootstrap

import (
	"context"
	"slices"
	"strings"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
)

func requireAdminTenant(ctx context.Context, tenant int64, method string) error {
	if _, err := scope.Query(method); err != nil {
		return err
	}
	a, err := scope.From(ctx)
	if err != nil || a.Tenant != tenant {
		return scope.Denied()
	}
	return nil
}

func (s *adminStore) validateUserTarget(ctx context.Context, owner, department int64) error {
	a, err := scope.From(ctx)
	if err != nil {
		return err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	var target model.Department
	if err := q.Select("id, path").Where("tenant_id = ? AND id = ?", a.Tenant, department).Take(&target).Error; err != nil {
		return adminDBError(err)
	}
	for _, role := range a.Roles {
		if manageableTarget(a, role, target, owner, department) {
			return nil
		}
	}
	return scope.Denied()
}

func manageableTarget(a scope.Access, role scope.Role, target model.Department, owner, department int64) bool {
	switch role.Kind {
	case "all":
		return true
	case "dept":
		return department == a.Department
	case "self":
		return owner == a.User && department == a.Department
	case "custom":
		return slices.Contains(role.Departments, department)
	case "dept_and_child":
		return a.Path != "" && (target.Path == a.Path || strings.HasPrefix(target.Path, a.Path+"."))
	default:
		return false
	}
}

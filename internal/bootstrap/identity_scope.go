package bootstrap

import (
	"context"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"gorm.io/gorm"
)

func identityScope(ctx context.Context, row any, q *gorm.DB) (*gorm.DB, error) {
	root, ok := row.(interface{ TableName() string })
	if !ok {
		return nil, scope.Denied()
	}
	d, err := scope.Lookup(root.TableName())
	if err != nil {
		return nil, err
	}
	a, err := scope.From(ctx)
	if err != nil {
		return nil, err
	}
	q = q.Where(d.Alias+"."+d.TenantColumn+" = ?", a.Tenant)
	if d.Shared {
		return q, nil
	}
	for _, role := range a.Roles {
		if role.Kind == "all" {
			return q, nil
		}
	}
	clauses, args := []string{}, []any{}
	for _, role := range a.Roles {
		predicate, values := rolePredicate(d, a, role)
		clauses, args = append(clauses, "("+predicate+")"), append(args, values...)
	}
	return q.Where("("+strings.Join(clauses, " OR ")+")", args...), nil
}

func rolePredicate(d scope.Declaration, a scope.Access, role scope.Role) (string, []any) {
	if role.Kind == "self" {
		return d.Alias + "." + d.OwnerColumn + " = ?", []any{a.User}
	}
	department := "sys_user.dept_id"
	if d.Entity == "sys_refresh_token" {
		department = "scope_user.dept_id"
	}
	var predicate string
	var args []any
	switch role.Kind {
	case "dept":
		predicate, args = department+" = ?", []any{a.Department}
	case "dept_and_child":
		if a.Path == "" {
			return "1 = 0", nil
		}
		escaped := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(a.Path)
		predicate = department + " IN (SELECT scope_dept.id FROM sys_dept scope_dept WHERE scope_dept.tenant_id = ? AND (scope_dept.path = ? OR scope_dept.path LIKE ? || '.%'))"
		args = []any{a.Tenant, a.Path, escaped}
	case "custom":
		if len(role.Departments) == 0 {
			return "1 = 0", nil
		}
		predicate, args = department+" IN ?", []any{role.Departments}
	default:
		return "1 = 0", nil
	}
	if d.Entity == "sys_refresh_token" {
		predicate = "sys_refresh_token.user_id IN (SELECT scope_user.id FROM sys_user scope_user WHERE scope_user.tenant_id = ? AND scope_user.deleted_at IS NULL AND " + predicate + ")"
		args = append([]any{a.Tenant}, args...)
	}
	return predicate, args
}

// Authorize loads current active account/role facts before constructing a trusted Scope.
func (s *adminStore) Authorize(ctx context.Context, tenant, user int64) (context.Context, error) {
	if _, err := scope.Query("Admin.Authorize"); err != nil {
		return ctx, err
	}
	if tenant <= 0 || user <= 0 {
		return ctx, scope.Denied()
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	var row model.User
	err := q.Select("sys_user.id, sys_user.tenant_id, sys_user.dept_id").
		Joins("JOIN tenant t ON t.id = sys_user.tenant_id AND t.status = ?", "active").
		Where("sys_user.tenant_id = ? AND sys_user.id = ? AND sys_user.status = ?", tenant, user, "active").Take(&row).Error
	if err != nil {
		return ctx, adminDBError(err)
	}
	policy, err := s.loadGrantPolicy(ctx, tenant, user)
	if err != nil {
		return ctx, err
	}
	a := scope.Access{Tenant: tenant, User: user, Department: row.DepartmentID}
	for _, dept := range policy.Departments {
		if dept.ID == row.DepartmentID {
			a.Path = dept.Path
		}
	}
	for _, role := range policy.Roles {
		a.Roles = append(a.Roles, scope.Role{Kind: role.DataScope, Departments: policy.CustomDepartmentIDs})
	}
	ctx = scope.WithAccess(ctx, a)
	if _, err := scope.From(ctx); err != nil {
		return ctx, err
	}
	return db.WithScope(ctx, identityScope), nil
}

func scopedAdminQuery(ctx context.Context, tenant int64, row any, query func(context.Context) (*gorm.DB, context.CancelFunc)) *db.ScopedQuery {
	// Even a caller carrying a valid context cannot substitute a different tenant argument.
	a, err := scope.From(ctx)
	if err != nil || a.Tenant != tenant {
		ctx = db.WithScope(ctx, func(context.Context, any, *gorm.DB) (*gorm.DB, error) { return nil, scope.Denied() })
	} else {
		ctx = db.WithScope(ctx, identityScope)
	}
	q, cancel := query(ctx)
	return db.BindScoped(ctx, row, q, cancel)
}

func (s *adminStore) GrantPolicy(ctx context.Context, tenant, user int64) (repo.GrantPolicy, error) {
	if _, err := scope.Query("Admin.GrantPolicy"); err != nil {
		return repo.GrantPolicy{}, err
	}
	a, err := scope.From(ctx)
	if err != nil || a.Tenant != tenant || a.User != user {
		return repo.GrantPolicy{}, scope.Denied()
	}
	return s.loadGrantPolicy(ctx, tenant, user)
}

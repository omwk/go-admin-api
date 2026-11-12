package bootstrap

import (
	"context"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/stretchr/testify/require"
)

func departmentDryCases() map[string]adminDryCase {
	return map[string]adminDryCase{
		"LockDepartments": {func(ctx context.Context, s *adminStore) error { _, err := s.LockDepartments(ctx, 17); return err }, "sys_dept"},
		"CreateDepartment": {func(ctx context.Context, s *adminStore) error {
			return s.CreateDepartment(ctx, &model.Department{TenantID: 17})
		}, "sys_dept"},
		"UpdateDepartment": {func(ctx context.Context, s *adminStore) error {
			return s.UpdateDepartment(ctx, 17, 29, map[string]any{"name": "changed"})
		}, "sys_dept"},
		"DeleteDepartment": {func(ctx context.Context, s *adminStore) error { return s.DeleteDepartment(ctx, 17, 29) }, "sys_dept"},
	}
}

func TestAdminS1DepartmentQueriesLockAndCheckTenant(t *testing.T) {
	// S1 §3.3: write graph reads use FOR UPDATE and tenant predicates for all effective scopes.
	for _, kind := range []string{"all", "dept", "dept_and_child", "self", "custom"} {
		store, capture := dryAdminStore(t)
		ctx := scope.WithAccess(context.Background(), matrixAccess(kind))
		_, err := store.LockDepartments(ctx, 17)
		require.NoError(t, err)
		require.Len(t, capture.statements, 1)
		require.Contains(t, capture.statements[0], "FOR UPDATE")
		require.Contains(t, capture.statements[0], "sys_dept.tenant_id = 17")
		require.NotContains(t, capture.statements[0], "SELECT *")
	}
	for _, run := range []func(context.Context, *adminStore) error{
		func(ctx context.Context, s *adminStore) error { _, err := s.LockDepartments(ctx, 18); return err },
		func(ctx context.Context, s *adminStore) error {
			return s.CreateDepartment(ctx, &model.Department{TenantID: 18})
		},
		func(ctx context.Context, s *adminStore) error {
			return s.UpdateDepartment(ctx, 18, 29, map[string]any{"name": "x"})
		},
		func(ctx context.Context, s *adminStore) error { return s.DeleteDepartment(ctx, 18, 29) },
	} {
		store, capture := dryAdminStore(t)
		ctx := scope.WithAccess(context.Background(), matrixAccess("all"))
		require.Error(t, run(ctx, store))
		require.Empty(t, capture.statements)
	}
}

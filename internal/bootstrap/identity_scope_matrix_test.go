package bootstrap

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/stretchr/testify/require"
)

type adminDryCase struct {
	run  func(context.Context, *adminStore) error
	root string
}

func adminDryCases() map[string]adminDryCase {
	p := repo.Page{Number: 1, Size: 20, Sort: "-created_at"}
	cases := map[string]adminDryCase{
		"ReadUser": {func(ctx context.Context, s *adminStore) error { _, err := s.ReadUser(ctx, 17, 29); return err }, "sys_user"},
		"ReadRole": {func(ctx context.Context, s *adminStore) error { _, err := s.ReadRole(ctx, 17, 29); return err }, "sys_role"},
		"ReadMenu": {func(ctx context.Context, s *adminStore) error { _, err := s.ReadMenu(ctx, 17, 29); return err }, "sys_menu"},
		"ReadLinks": {func(ctx context.Context, s *adminStore) error {
			_, err := s.ReadLinks(ctx, 17, repo.UserRoles, 29)
			return err
		}, "sys_user"},
		"Departments": {func(ctx context.Context, s *adminStore) error { _, err := s.Departments(ctx, 17, 0); return err }, "sys_dept"},
		"AffectedUsers": {func(ctx context.Context, s *adminStore) error {
			_, e := s.AffectedUsers(ctx, 17, repo.AuthzChange{Resource: "roles", ID: 29})
			return e
		}, ""},
		"Authorize": {func(ctx context.Context, s *adminStore) error { _, e := s.Authorize(ctx, 17, 29); return e }, ""},
		"Transaction": {func(ctx context.Context, s *adminStore) error {
			return s.Transaction(ctx, func(context.Context) error { return nil })
		}, ""},
		"LockManagement": {func(ctx context.Context, s *adminStore) error { return s.LockManagement(ctx, 17) }, ""},
		"Users":          {func(ctx context.Context, s *adminStore) error { _, _, e := s.Users(ctx, 17, p); return e }, "sys_user"},
		"User":           {func(ctx context.Context, s *adminStore) error { _, e := s.User(ctx, 17, 29); return e }, "sys_user"},
		"CreateUser": {func(ctx context.Context, s *adminStore) error {
			return s.CreateUser(ctx, &model.User{TenantID: 17, DepartmentID: 31})
		}, "sys_user"},
		"UpdateUser": {func(ctx context.Context, s *adminStore) error {
			return s.UpdateUser(ctx, 17, 29, map[string]any{"status": "active"})
		}, "sys_user"},
		"DeleteUser": {func(ctx context.Context, s *adminStore) error { return s.DeleteUser(ctx, 17, 29) }, "sys_user"},
		"Roles": {func(ctx context.Context, s *adminStore) error {
			_, _, e := s.Roles(ctx, 17, repo.Page{Number: 1, Size: 20, Sort: "-id"})
			return e
		}, "sys_role"},
		"Role":       {func(ctx context.Context, s *adminStore) error { _, e := s.Role(ctx, 17, 29); return e }, "sys_role"},
		"CreateRole": {func(ctx context.Context, s *adminStore) error { return s.CreateRole(ctx, &model.Role{TenantID: 17}) }, "sys_role"},
		"UpdateRole": {func(ctx context.Context, s *adminStore) error {
			return s.UpdateRole(ctx, 17, 29, map[string]any{"name": "changed"})
		}, "sys_role"},
		"DeleteRole": {func(ctx context.Context, s *adminStore) error { return s.DeleteRole(ctx, 17, 29) }, "sys_role"},
		"Menus":      {func(ctx context.Context, s *adminStore) error { _, e := s.Menus(ctx, 17); return e }, "sys_menu"},
		"Menu":       {func(ctx context.Context, s *adminStore) error { _, e := s.Menu(ctx, 17, 29); return e }, "sys_menu"},
		"CreateMenu": {func(ctx context.Context, s *adminStore) error { return s.CreateMenu(ctx, &model.Menu{TenantID: 17}) }, "sys_menu"},
		"UpdateMenu": {func(ctx context.Context, s *adminStore) error {
			return s.UpdateMenu(ctx, 17, 29, map[string]any{"name": "changed"})
		}, "sys_menu"},
		"DeleteMenu":  {func(ctx context.Context, s *adminStore) error { return s.DeleteMenu(ctx, 17, 29) }, "sys_menu"},
		"Permissions": {func(ctx context.Context, s *adminStore) error { _, e := s.Permissions(ctx, 17); return e }, "sys_permission"},
		"References":  {func(ctx context.Context, s *adminStore) error { _, e := s.References(ctx, 17, "role", 29); return e }, ""},
		"GrantPolicy": {func(ctx context.Context, s *adminStore) error { _, e := s.GrantPolicy(ctx, 17, 29); return e }, ""},
		"Links":       {func(ctx context.Context, s *adminStore) error { _, e := s.Links(ctx, 17, repo.UserRoles, 29); return e }, "sys_user"},
		"ReplaceLinks": {func(ctx context.Context, s *adminStore) error {
			return s.ReplaceLinks(ctx, 17, repo.UserRoles, 29, nil)
		}, "sys_user"},
		"Replay": {func(ctx context.Context, s *adminStore) error {
			_, _, e := s.Replay(ctx, 17, "scope", "key", "")
			return e
		}, ""},
		"SaveResponse": {func(ctx context.Context, s *adminStore) error {
			return s.SaveResponse(ctx, 17, "scope", "key", "hash", repo.SavedResponse{Status: 204})
		}, ""},
	}
	for name, tc := range departmentDryCases() {
		cases[name] = tc
	}
	return cases
}

func dryAdminStore(t *testing.T) (*adminStore, *sqlCapture) {
	t.Helper()
	base, capture := dryAuthStore(t)
	return &adminStore{raw: base.query, transaction: base.transaction}, capture
}

func matrixAccess(kind string) scope.Access {
	return scope.Access{Tenant: 17, User: 29, Department: 31, Path: "1.31", Roles: []scope.Role{{Kind: kind, Departments: []int64{31, 32}}}}
}

func TestAdminRepositoryReflectionDryRunMatrix(t *testing.T) {
	// REQ-IAM-008: every interface method is registered exactly once, including helpers.
	cases := adminDryCases()
	names := []string{}
	for name := range cases {
		names = append(names, name)
	}
	contract := reflect.TypeOf((*repo.Admin)(nil)).Elem()
	assertRegisteredMethods(t, contract, names)
	assertRegisteredMethods(t, contract, scope.QueryNames("Admin"))
	t.Logf("repo.Admin 方法数 = %d; DryRun 登记数 = %d", contract.NumMethod(), len(cases))
	for _, kind := range []string{"all", "dept", "dept_and_child", "self", "custom"} {
		for name, tc := range cases {
			t.Run(kind+"/"+name, func(t *testing.T) {
				runAdminDryCase(t, name, kind, tc)
			})
		}
	}
}

func runAdminDryCase(t *testing.T, name, kind string, tc adminDryCase) {
	t.Helper()
	store, capture := dryAdminStore(t)
	err := tc.run(scope.WithAccess(context.Background(), matrixAccess(kind)), store)
	if name == "Authorize" {
		require.Error(t, err)
		return
	} // DryRun has no effective role rows.
	if name == "CreateUser" && (kind == "self" || kind == "dept_and_child") {
		require.Error(t, err)
		return
	}
	if strings.HasPrefix(name, "Update") || strings.HasPrefix(name, "Delete") {
		require.Error(t, err)
	} else {
		require.NoError(t, err)
	}
	assertAdminDryStatements(t, name, kind, tc.root, capture.statements)
}

func assertAdminDryStatements(t *testing.T, name, kind, root string, statements []string) {
	t.Helper()
	for _, sql := range statements {
		require.NotContains(t, sql, "SELECT *")
		if root != "" && strings.Contains(sql, `"`+root+`"`) && !strings.HasPrefix(sql, "INSERT") {
			assertRootScope(t, sql, root, kind)
		}
	}
	if name == "Users" {
		require.Len(t, statements, 2)
		require.Contains(t, statements[0], "COUNT(DISTINCT")
		for _, sql := range statements {
			assertRootScope(t, sql, "sys_user", kind)
		}
	}
}

func assertRootScope(t *testing.T, sql, root, kind string) {
	t.Helper()
	require.Contains(t, sql, root+".tenant_id = 17")
	if root != "sys_user" && root != "sys_refresh_token" {
		return
	}
	switch kind {
	case "self":
		owner := "id"
		if root == "sys_refresh_token" {
			owner = "user_id"
		}
		require.Contains(t, sql, root+"."+owner+" = 29")
	case "dept":
		require.Contains(t, sql, "dept_id = 31")
	case "custom":
		require.Contains(t, sql, "dept_id IN (31,32)")
	case "dept_and_child":
		require.Contains(t, sql, "scope_dept.tenant_id = 17")
		require.Contains(t, sql, "scope_dept.path = '1.31' OR scope_dept.path LIKE '1.31' || '.%'")
		require.NotContains(t, sql, "LIKE '1.31%'")
	}
}

func TestAdminMissingIdentityDeniesProtectedMethods(t *testing.T) {
	for name, tc := range adminDryCases() {
		if name == "Transaction" || name == "Authorize" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			s, capture := dryAdminStore(t)
			require.Error(t, tc.run(context.Background(), s))
			require.Empty(t, capture.statements)
		})
	}
}

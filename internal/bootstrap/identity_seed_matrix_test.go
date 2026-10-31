package bootstrap

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/stretchr/testify/require"
)

func TestSeedRepositoryReflectionDryRunMatrix(t *testing.T) {
	password, err := refresh.Generate()
	require.NoError(t, err)
	hash, err := pwd.Hash(password)
	require.NoError(t, err)
	path := "/matrix-seed"
	cases := map[string]struct {
		run       func(*seedStore) error
		fragments []string
	}{
		"Transaction": {func(s *seedStore) error {
			return s.Transaction(context.Background(), func(context.Context) error { return nil })
		}, nil},
		"LockSeed": {func(s *seedStore) error { return s.LockSeed(context.Background(), 17) }, []string{"identity:seed:17", "identity:management:17"}},
		"SeedPermissions": {func(s *seedStore) error {
			return s.SeedPermissions(context.Background(), 17, []model.Permission{{Code: permission.SystemUserList, Module: "system", Type: "api"}})
		}, []string{"INSERT INTO", "tenant_id", "17", "ON CONFLICT"}},
		"SeedMenus": {func(s *seedStore) error {
			return s.SeedMenus(context.Background(), 17, []repo.SeedMenu{{Row: model.Menu{Path: &path, Type: "menu"}}})
		}, []string{"tenant_id = 17", "path = '/matrix-seed'"}},
		"SeedRoles": {func(s *seedStore) error {
			return s.SeedRoles(context.Background(), 17, []repo.SeedRole{{Row: model.Role{Code: "matrix", DataScope: "self"}}})
		}, []string{"tenant_id = 17", "code = 'matrix'", "INSERT INTO"}},
		"SeedAdmin": {func(s *seedStore) error {
			return s.SeedAdmin(context.Background(), 17, repo.AdminSeed{Username: "matrix", DepartmentName: "matrix", RoleCode: "matrix", PasswordHash: hash})
		}, []string{"tenant_id = 17", "username = 'matrix'", "parent_id = 0"}},
	}
	names := []string{}
	for name, tc := range cases {
		names = append(names, name)
		t.Run(name, func(t *testing.T) {
			base, capture := dryAuthStore(t)
			require.NoError(t, tc.run(&seedStore{authStore: base}))
			for _, fragment := range tc.fragments {
				require.Contains(t, strings.Join(capture.statements, "\n"), fragment)
			}
			for _, sql := range capture.statements {
				require.NotContains(t, sql, "SELECT *")
			}
		})
	}
	contract := reflect.TypeOf((*repo.Seed)(nil)).Elem()
	assertRegisteredMethods(t, contract, names)
	assertRegisteredMethods(t, contract, scope.QueryNames("Seed"))
	t.Logf("repo.Seed 方法数 = %d; DryRun 登记数 = %d", contract.NumMethod(), len(cases))
}

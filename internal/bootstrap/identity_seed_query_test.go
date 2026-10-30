package bootstrap

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

func TestSeedRepositoryDryRunMatrixMatchesInterface(t *testing.T) {
	ctx := context.Background()
	password, err := refresh.Generate()
	require.NoError(t, err)
	hash, err := pwd.Hash(password)
	require.NoError(t, err)
	path := "/sample"
	cases := map[string]func(*seedStore) error{
		"Transaction": func(s *seedStore) error { return s.Transaction(ctx, func(context.Context) error { return nil }) },
		"LockSeed":    func(s *seedStore) error { return s.LockSeed(ctx, 17) },
		"SeedPermissions": func(s *seedStore) error {
			return s.SeedPermissions(ctx, 17, []model.Permission{{Code: permission.SystemMenuList, Name: "menu", Module: "system", Type: "api"}})
		},
		"SeedMenus": func(s *seedStore) error {
			return s.SeedMenus(ctx, 17, []repo.SeedMenu{{Row: model.Menu{Path: &path, Type: "menu", Status: "active", Visible: true}}})
		},
		"SeedRoles": func(s *seedStore) error {
			return s.SeedRoles(ctx, 17, []repo.SeedRole{{Row: model.Role{Code: identity.OperatorRoleCode, Name: "operator", Status: "active", DataScope: "self"}}})
		},
		"SeedAdmin": func(s *seedStore) error {
			return s.SeedAdmin(ctx, 17, repo.AdminSeed{Username: identity.SeedAdminUsername, DepartmentName: identity.SeedDepartmentName, RoleCode: identity.SuperAdminRoleCode, PasswordHash: hash})
		},
	}
	registered := []string{}
	for name, run := range cases {
		registered = append(registered, name)
		t.Run(name, func(t *testing.T) {
			base, capture := dryAuthStore(t)
			require.NoError(t, run(&seedStore{authStore: base}))
			if name == "Transaction" {
				require.Empty(t, capture.statements)
				return
			}
			require.NotEmpty(t, capture.statements)
			for _, query := range capture.statements {
				require.NotContains(t, query, "SELECT *")
				if strings.Contains(query, "SELECT pg_advisory") {
					require.Contains(t, query, "identity:seed:17")
					continue
				}
				require.Contains(t, query, "tenant_id")
				if strings.HasPrefix(query, "SELECT") {
					require.Contains(t, query, "tenant_id = 17")
				}
			}
		})
	}
	assertRegisteredMethods(t, reflect.TypeOf((*repo.Seed)(nil)).Elem(), registered)
}

func TestSeedIntegrationRejectsSuperAdminDataScopeDrift(t *testing.T) {
	f, _ := seededProfileFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id = ? AND code = ?", 1, identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	require.ErrorContains(t, SeedIdentity(f.ctx, f.database, f.password), "data_scope all")
}

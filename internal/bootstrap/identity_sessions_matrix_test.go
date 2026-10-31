package bootstrap

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/stretchr/testify/require"
)

func TestSessionsRepositoryReflectionDryRunMatrix(t *testing.T) {
	cases := map[string]func(context.Context, *sessionStore) error{
		"Sessions": func(ctx context.Context, s *sessionStore) error {
			_, _, err := s.Sessions(ctx, 17, repo.Page{Number: 1, Size: 20}, time.Now())
			return err
		},
		"SessionFamily": func(ctx context.Context, s *sessionStore) error {
			_, err := s.SessionFamily(ctx, 17, "known-sid")
			return err
		},
	}
	names := []string{}
	for name := range cases {
		names = append(names, name)
	}
	contract := reflect.TypeOf((*repo.Sessions)(nil)).Elem()
	assertRegisteredMethods(t, contract, names)
	assertRegisteredMethods(t, contract, scope.QueryNames("Sessions"))
	t.Logf("repo.Sessions 方法数 = %d; DryRun 登记数 = %d", contract.NumMethod(), len(cases))
	for _, kind := range []string{"all", "dept", "dept_and_child", "self", "custom"} {
		for name, run := range cases {
			t.Run(kind+"/"+name, func(t *testing.T) {
				admin, capture := dryAdminStore(t)
				err := run(scope.WithAccess(context.Background(), matrixAccess(kind)), &sessionStore{admin: admin})
				if name == "SessionFamily" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.Len(t, capture.statements, 2)
				}
				for _, sql := range capture.statements {
					assertRootScope(t, sql, "sys_refresh_token", kind)
					if kind == "dept" || kind == "dept_and_child" || kind == "custom" {
						require.Contains(t, sql, "scope_user.tenant_id = 17")
					}
					if name == "Sessions" {
						require.Contains(t, sql, "terminal.tenant_id = 17")
						require.Contains(t, sql, "revoked_at IS NULL")
						require.Contains(t, sql, "expires_at >")
					}
				}
			})
		}
	}
	for name, run := range cases {
		t.Run("missing/"+name, func(t *testing.T) {
			admin, capture := dryAdminStore(t)
			require.Error(t, run(context.Background(), &sessionStore{admin: admin}))
			require.Empty(t, capture.statements)
		})
	}
}

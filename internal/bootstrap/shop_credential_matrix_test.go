package bootstrap

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
	"github.com/stretchr/testify/require"
)

type credentialDryCase func(context.Context, *shopStore) error

func credentialDryCases() map[string]credentialDryCase {
	audit := model.CredentialAudit{Action: "read", Result: "success", ActorType: "user", Detail: []byte(`{"metadata_only":true}`), TraceID: "matrix"}
	return map[string]credentialDryCase{
		"CredentialTransaction": func(context.Context, *shopStore) error { return nil }, // Transaction commit/rollback is covered against PostgreSQL.
		"Credentials": func(ctx context.Context, s *shopStore) error {
			_, _, err := s.Credentials(ctx, 17, 41, credentialTestPage(), audit)
			return err
		},
		"Credential": func(ctx context.Context, s *shopStore) error { _, err := s.Credential(ctx, 17, 43, audit); return err },
		"CredentialAudits": func(ctx context.Context, s *shopStore) error {
			_, _, err := s.CredentialAudits(ctx, 17, 43, credentialTestPage(), audit)
			return err
		},
		"CheckCredential": func(ctx context.Context, s *shopStore) error { return s.CheckCredential(ctx, 17, 43, audit) },
		"DisableCredential": func(ctx context.Context, s *shopStore) error {
			_, err := s.DisableCredential(ctx, 17, 43, audit)
			return err
		},
		"RotateCredential": func(ctx context.Context, s *shopStore) error {
			_, err := s.RotateCredential(ctx, 17, 43, func(row model.Credential, _ int) (*model.CredentialVersion, model.Credential, error) {
				return nil, row, nil
			}, audit)
			return err
		},
		"CreateCredential": func(ctx context.Context, s *shopStore) error {
			row := model.Credential{ID: 43, TenantID: 17, ShopID: 41, Kind: "oauth2", Status: "active", Extra: []byte(`{}`)}
			_, err := s.CreateCredential(ctx, 17, 41, &row, func(id int64, n int) (*model.CredentialVersion, error) {
				return &model.CredentialVersion{ID: 47, TenantID: 17, CredentialID: id, VersionNo: n}, nil
			}, audit)
			return err
		},
	}
}

func TestCredentialRepositoryReflectionAndFiveScopeDryRunMatrix(t *testing.T) {
	// REQ-IAM-008: declarations, port methods and matrix registration must stay exactly aligned.
	cases := credentialDryCases()
	names := make([]string, 0, len(cases))
	for name := range cases {
		names = append(names, name)
	}
	contract := reflect.TypeOf((*repo.CredentialStore)(nil)).Elem()
	assertRegisteredMethods(t, contract, names)
	assertRegisteredMethods(t, contract, scopeRegistryQueryNames("Credential"))
	for _, kind := range []string{"all", "self", "dept", "dept_and_child", "custom"} {
		for name, run := range cases {
			t.Run(kind+"/"+name, func(t *testing.T) {
				if name == "CredentialTransaction" {
					return
				}
				s, capture := dryShopStore(t)
				// DryRun cannot populate the current pointer. Writes stop safely after their scoped query.
				if err := run(scope.WithAccess(context.Background(), matrixAccess(kind)), s); err != nil {
					require.Contains(t, []string{"CreateCredential", "RotateCredential", "DisableCredential", "CheckCredential"}, name)
				}
				require.NotEmpty(t, capture.statements)
				for _, sql := range capture.statements {
					if strings.Contains(sql, `FROM "shop"`) {
						assertCredentialRoot(t, sql, kind)
					}
					require.NotContains(t, sql, "SELECT *")
				}
			})
		}
	}
}

func assertCredentialRoot(t *testing.T, sql, kind string) {
	t.Helper()
	require.Contains(t, sql, "shop.tenant_id = 17")
	require.Contains(t, sql, "shop.deleted_at IS NULL")
	switch kind {
	case "self":
		require.Contains(t, sql, "shop.owner_user_id = 29")
	case "dept":
		require.Contains(t, sql, "shop.dept_id = 31")
	case "custom":
		require.Contains(t, sql, "shop.dept_id IN (31,32)")
	case "dept_and_child":
		require.Contains(t, sql, "scope_dept.tenant_id = 17")
		require.Contains(t, sql, "scope_dept.path = '1.31'")
	}
	if strings.Contains(sql, "JOIN shop_credential") {
		require.Contains(t, sql, "shop_credential.tenant_id = shop.tenant_id")
		require.Contains(t, sql, "shop_credential.shop_id = shop.id")
		require.Contains(t, sql, "cv.credential_id = shop_credential.id")
	}
}

func TestCredentialRepositoryRejectsMissingIdentityAndMismatchedTenant(t *testing.T) {
	for name, run := range credentialDryCases() {
		if name == "CredentialTransaction" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			for _, ctx := range []context.Context{context.Background(), scope.WithAccess(context.Background(), scope.Access{Tenant: 9, User: 29, Roles: []scope.Role{{Kind: "all"}}})} {
				s, capture := dryShopStore(t)
				require.Error(t, run(ctx, s))
				require.Empty(t, capture.statements)
			}
		})
	}
}

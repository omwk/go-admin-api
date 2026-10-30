package identity

import (
	"context"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

type seedRecorder struct {
	calls []string
	admin repo.AdminSeed
}

func (s *seedRecorder) Transaction(ctx context.Context, fn func(context.Context) error) error {
	s.calls = append(s.calls, "transaction")
	return fn(ctx)
}
func (s *seedRecorder) LockSeed(context.Context, int64) error {
	s.calls = append(s.calls, "lock")
	return nil
}
func (s *seedRecorder) SeedPermissions(_ context.Context, _ int64, rows []model.Permission) error {
	s.calls = append(s.calls, "permissions")
	for _, row := range rows {
		if err := permission.Validate(row.Module, row.Code); err != nil {
			return err
		}
	}
	return nil
}
func (s *seedRecorder) SeedMenus(context.Context, int64, []repo.SeedMenu) error {
	s.calls = append(s.calls, "menus")
	return nil
}
func (s *seedRecorder) SeedRoles(context.Context, int64, []repo.SeedRole) error {
	s.calls = append(s.calls, "roles")
	return nil
}
func (s *seedRecorder) SeedAdmin(_ context.Context, _ int64, admin repo.AdminSeed) error {
	s.calls = append(s.calls, "admin")
	s.admin = admin
	return nil
}

func TestSeedOrderAndPasswordHash(t *testing.T) {
	store := &seedRecorder{}
	service, err := NewSeedService(store)
	require.NoError(t, err)
	password, err := refresh.Generate()
	require.NoError(t, err)
	require.NoError(t, service.Run(context.Background(), 1, password))
	require.Equal(t, []string{"transaction", "lock", "permissions", "menus", "roles", "admin"}, store.calls)
	require.NoError(t, pwd.Verify(store.admin.PasswordHash, password))
	require.Error(t, service.Run(context.Background(), 1, ""))
}

func TestSeedSuperAdminDataScopeInvariant(t *testing.T) {
	// ADR-011: all data comes from the explicit seed role, never an is_super scope bypass.
	found := false
	for _, role := range seedRoles() {
		if role.Row.Code == SuperAdminRoleCode {
			found = true
			require.Equal(t, "all", role.Row.DataScope)
		}
	}
	require.True(t, found)
}

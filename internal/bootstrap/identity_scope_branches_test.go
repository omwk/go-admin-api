package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdminDryRunPrivateRelationshipBranches(t *testing.T) {
	ctx := scope.WithAccess(context.Background(), matrixAccess("self"))
	for _, kind := range []repo.LinkKind{repo.UserRoles, repo.RoleMenus, repo.RolePermissions, repo.RoleDepartments} {
		t.Run(string(kind), func(t *testing.T) {
			s, capture := dryAdminStore(t)
			_, err := s.Links(ctx, 17, kind, 29)
			require.NoError(t, err)
			require.NoError(t, s.ReplaceLinks(ctx, 17, kind, 29, nil))
			require.Error(t, s.ReplaceLinks(ctx, 17, kind, 29, []int64{31})) // DryRun cannot produce target existence rows.
			for _, sql := range capture.statements {
				require.Contains(t, sql, "tenant_id = 17")
				require.NotContains(t, sql, "SELECT *")
			}
			if kind == repo.UserRoles {
				require.Contains(t, strings.Join(capture.statements, "\n"), "sys_user.id = 29")
			}
		})
	}
	for _, resource := range []string{"role", "menu"} {
		s, capture := dryAdminStore(t)
		_, err := s.References(ctx, 17, resource, 29)
		require.NoError(t, err)
		for _, sql := range capture.statements {
			require.Contains(t, sql, "tenant_id = 17")
		}
	}
	for _, resource := range []string{"users", "roles", "menus"} {
		s, capture := dryAdminStore(t)
		_, err := s.AffectedUsers(ctx, 17, repo.AuthzChange{Resource: resource, ID: 29})
		require.NoError(t, err)
		for _, sql := range capture.statements {
			require.Contains(t, sql, "tenant_id = 17")
		}
	}
	s, capture := dryAdminStore(t)
	_, _, err := s.Replay(ctx, 17, "scope", "key", "different")
	require.Error(t, err)
	require.Len(t, capture.statements, 1)
}

func TestScopeIntegrationOneToManyDeduplicatesRootUsers(t *testing.T) {
	f := newAdminFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var roles []model.Role
	require.NoError(t, q.Select("id").Where("tenant_id = ?", 1).Limit(2).Find(&roles).Error)
	require.Len(t, roles, 2)
	for _, role := range roles {
		require.NoError(t, q.Create(&model.UserRole{TenantID: 1, UserID: f.user.ID, RoleID: role.ID}).Error)
	}
	query := f.store.raw
	f.store.raw = func(ctx context.Context) (*gorm.DB, context.CancelFunc) {
		q, cancel := query(ctx)
		return q.Joins("JOIN sys_user_role matrix_link ON matrix_link.tenant_id = sys_user.tenant_id AND matrix_link.user_id = sys_user.id"), cancel
	}
	rows, count, err := f.store.Users(f.ctx, 1, repo.Page{Number: 1, Size: 100, Sort: "-created_at"})
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	require.Len(t, rows, 2)
}

func TestCachedAccountDryRunRemainsUncachedAndIdentityBound(t *testing.T) {
	base, capture := dryAuthStore(t)
	store := &profileStore{query: base.query}
	require.Error(t, store.account(context.Background(), 17, 29))
	require.Len(t, capture.statements, 1)
	require.Contains(t, capture.statements[0], "sys_user.tenant_id = 17 AND sys_user.id = 29")
	require.Contains(t, capture.statements[0], "sys_user.status = 'active'")
	capture.statements = nil
	require.Error(t, store.account(context.Background(), 0, 0))
	require.Empty(t, capture.statements)
}

func TestScopeUnknownMethodsAndModelsRefuseExecution(t *testing.T) {
	s, capture := dryAdminStore(t)
	ctx := scope.WithAccess(context.Background(), matrixAccess("all"))
	require.Error(t, requireAdminTenant(ctx, 17, "Admin.Unregistered"))
	q, cancel := s.query(ctx, 17, &model.User{}, "Admin.Unregistered")
	defer cancel()
	require.Error(t, q.Find(&[]model.User{}))
	q2, cancel2 := s.query(ctx, 17, &model.RefreshToken{}, "Admin.User")
	defer cancel2()
	require.Error(t, q2.Find(&[]model.RefreshToken{}))
	require.Empty(t, capture.statements)
}

package bootstrap

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

func TestProfileRepositoryDryRunMatrixMatchesInterface(t *testing.T) {
	cases := map[string]struct {
		run       func(*profileStore) error
		fragments []string
	}{
		"Identity":  {run: func(s *profileStore) error { _, err := s.Identity(context.Background(), 17, 29); return err }, fragments: []string{"u.tenant_id = 17 AND u.id = 29", "u.deleted_at IS NULL", "u.status = 'active'", "u.is_super OR p.id = rp.permission_id", "r.status = 'active'"}},
		"Menus":     {run: func(s *profileStore) error { _, err := s.Menus(context.Background(), 17); return err }, fragments: []string{"tenant_id = 17", "status = 'active'", "visible = true", "type <> 'button'"}},
		"RoleMenus": {run: func(s *profileStore) error { _, err := s.RoleMenus(context.Background(), 17, 29); return err }, fragments: []string{"rm.tenant_id = 17 AND ur.user_id = 29", "r.status = 'active'", "DISTINCT"}},
	}
	registered := []string{}
	for name, tc := range cases {
		registered = append(registered, name)
		t.Run(name, func(t *testing.T) {
			base, capture := dryAuthStore(t)
			store := &profileStore{query: base.query}
			require.NoError(t, tc.run(store))
			require.Len(t, capture.statements, 1)
			for _, fragment := range tc.fragments {
				require.Contains(t, capture.statements[0], fragment)
			}
			require.NotContains(t, capture.statements[0], "password_hash")
			require.NotContains(t, capture.statements[0], "SELECT *")
		})
	}
	assertRegisteredMethods(t, reflect.TypeOf((*repo.Profile)(nil)).Elem(), registered)
	assertRegisteredMethods(t, reflect.TypeOf((*repo.Profile)(nil)).Elem(), scope.QueryNames("Profile"))
	t.Logf("repo.Profile 方法数 = %d; DryRun 登记数 = %d", len(registered), len(cases))
}

func assertRegisteredMethods(t *testing.T, contract reflect.Type, registered []string) {
	t.Helper()
	methods := []string{}
	for i := 0; i < contract.NumMethod(); i++ {
		methods = append(methods, contract.Method(i).Name)
	}
	sort.Strings(methods)
	sort.Strings(registered)
	require.Equal(t, methods, registered)
}

func TestProfileIntegrationUsesExactlyTwoMenuQueries(t *testing.T) {
	f, _ := seededProfileFixture(t)
	assignProfileRole(t, f, identity.OperatorRoleCode)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	rows := make([]model.Menu, 0, 30)
	for i := 0; i < 30; i++ {
		rows = append(rows, model.Menu{TenantID: 1, Name: "additional", Type: "menu", Status: "active", Visible: true})
	}
	require.NoError(t, q.CreateInBatches(rows, 30).Error)
	capture := &sqlCapture{Interface: gormlog.Discard}
	store := &profileStore{query: func(ctx context.Context) (*gorm.DB, context.CancelFunc) {
		q, cancel := f.database.WithContext(ctx)
		return q.Session(&gorm.Session{Logger: capture}), cancel
	}}
	service, err := identity.NewProfileService(store)
	require.NoError(t, err)
	_, err = service.Menus(f.ctx, 1, f.user.ID)
	require.NoError(t, err)
	require.Len(t, capture.statements, 3, "one identity query plus exactly two menu queries regardless of menu count")
	require.Contains(t, capture.statements[1], `FROM "sys_menu"`)
	require.Contains(t, capture.statements[2], "sys_role_menu rm")
	for _, query := range capture.statements {
		require.False(t, strings.Contains(query, "password_hash"))
	}
	foreign, err := store.Identity(f.ctx, 2, f.user.ID)
	require.NoError(t, err)
	require.Empty(t, foreign)
}

func TestProfileRepositoryRejectsMissingIdentity(t *testing.T) {
	base, capture := dryAuthStore(t)
	store := &profileStore{query: base.query}
	_, err := store.Identity(context.Background(), 1, 0)
	require.Error(t, err)
	_, err = store.Menus(context.Background(), 0)
	require.Error(t, err)
	_, err = store.RoleMenus(context.Background(), 1, 0)
	require.Error(t, err)
	require.Empty(t, capture.statements)
}

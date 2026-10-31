package bootstrap

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/identity/session"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

type sqlCapture struct {
	gormlog.Interface
	statements []string
}

func (c *sqlCapture) Trace(_ context.Context, _ time.Time, sql func() (string, int64), _ error) {
	statement, _ := sql()
	c.statements = append(c.statements, statement)
}

func dryAuthStore(t *testing.T) (*authStore, *sqlCapture) {
	t.Helper()
	capture := &sqlCapture{Interface: gormlog.Discard}
	base, err := gorm.Open(postgres.Open(""), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: capture})
	require.NoError(t, err)
	return &authStore{
		query: func(ctx context.Context) (*gorm.DB, context.CancelFunc) {
			return base.Session(&gorm.Session{NewDB: true}).WithContext(ctx), func() {}
		},
		transaction: func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) },
	}, capture
}

func TestAuthRepositoryDryRunMatrixMatchesEveryInterfaceMethod(t *testing.T) {
	// REQ-IAM-001/002/010: auth queries use dedicated identity bounds, never a generic bypass.
	sid, err := session.NewID()
	require.NoError(t, err)
	raw, err := refresh.Generate()
	require.NoError(t, err)
	row := model.RefreshToken{ID: 3, TenantID: 17, UserID: 29, SessionID: sid, TokenHash: refresh.Hash(raw), ExpiresAt: time.Now().Add(time.Hour)}
	cases := map[string]struct {
		run       func(*authStore) error
		fragments []string
	}{
		"Transaction": {run: func(s *authStore) error {
			return s.Transaction(context.Background(), func(context.Context) error { return nil })
		}},
		"UserForLogin": {run: func(s *authStore) error {
			_, _, err := s.UserForLogin(context.Background(), 17, "operator")
			return err
		}, fragments: []string{"sys_user.tenant_id = 17", "sys_user.username = 'operator'", "tenant.status = 'active'", "deleted_at", "FOR UPDATE OF"}},
		"SaveLogin": {run: func(s *authStore) error {
			return s.SaveLogin(context.Background(), model.User{ID: 29, TenantID: 17, Status: "active"})
		}, fragments: []string{"UPDATE", "tenant_id = 17 AND id = 29", "deleted_at"}},
		"RefreshByHash": {run: func(s *authStore) error {
			_, _, err := s.RefreshByHash(context.Background(), row.TokenHash)
			return err
		}, fragments: []string{"WHERE token_hash = '" + row.TokenHash + "'"}},
		"LockSession":    {run: func(s *authStore) error { return s.LockSession(context.Background(), 17, sid) }, fragments: []string{"pg_advisory_xact_lock", "identity:session:17:" + sid}},
		"Family":         {run: func(s *authStore) error { _, err := s.Family(context.Background(), 17, 29, sid); return err }, fragments: []string{"tenant_id = 17 AND user_id = 29 AND session_id = '" + sid + "'"}},
		"SessionRevoked": {run: func(s *authStore) error { _, err := s.SessionRevoked(context.Background(), 17, 29, sid); return err }, fragments: []string{"EXISTS", "tenant_id = 17 AND user_id = 29 AND session_id = '" + sid + "'"}},
		"InsertRefresh":  {run: func(s *authStore) error { copied := row; return s.InsertRefresh(context.Background(), &copied) }, fragments: []string{"INSERT INTO", "tenant_id", "user_id", "session_id", sid}},
		"ClaimRefresh":   {run: func(s *authStore) error { _, err := s.ClaimRefresh(context.Background(), row, time.Now()); return err }, fragments: []string{"tenant_id = 17 AND user_id = 29 AND session_id = '" + sid + "' AND id = 3", "revoked_at IS NULL", "expires_at >", "rotated"}},
		"LinkRefresh":    {run: func(s *authStore) error { return s.LinkRefresh(context.Background(), row, 4) }, fragments: []string{"tenant_id = 17 AND user_id = 29 AND session_id = '" + sid + "' AND id = 3", "replaced_by_token_id", "rotated"}},
		"RevokeFamily": {run: func(s *authStore) error {
			return s.RevokeFamily(context.Background(), 17, 29, sid, "logout", time.Now())
		}, fragments: []string{"tenant_id = 17 AND user_id = 29 AND session_id = '" + sid + "'", "COALESCE(revoked_at", "logout"}},
	}
	registered, methods := make([]string, 0, len(cases)), []string{}
	contract := reflect.TypeOf((*repo.Auth)(nil)).Elem()
	for i := 0; i < contract.NumMethod(); i++ {
		methods = append(methods, contract.Method(i).Name)
	}
	for name, tc := range cases {
		registered = append(registered, name)
		t.Run(name, func(t *testing.T) {
			store, capture := dryAuthStore(t)
			require.NoError(t, tc.run(store))
			if name == "Transaction" {
				require.Empty(t, capture.statements)
				return
			}
			require.Len(t, capture.statements, 1)
			for _, fragment := range tc.fragments {
				require.Contains(t, capture.statements[0], fragment)
			}
			require.NotContains(t, capture.statements[0], "SELECT *")
		})
	}
	sort.Strings(registered)
	sort.Strings(methods)
	require.Equal(t, methods, registered)
	assertRegisteredMethods(t, contract, scope.QueryNames("Auth"))
	t.Logf("repo.Auth 方法数 = %d; DryRun 登记数 = %d", contract.NumMethod(), len(cases))
}

func TestAuthRepositoryRejectsMissingIdentityBounds(t *testing.T) {
	ctx := context.Background()
	store, capture := dryAuthStore(t)
	_, _, err := store.UserForLogin(ctx, 0, "operator")
	require.Error(t, err)
	require.Error(t, store.SaveLogin(ctx, model.User{}))
	_, _, err = store.RefreshByHash(ctx, "")
	require.Error(t, err)
	require.Error(t, store.LockSession(ctx, 0, ""))
	_, err = store.Family(ctx, 1, 0, "")
	require.Error(t, err)
	require.Error(t, store.InsertRefresh(ctx, &model.RefreshToken{}))
	_, err = store.ClaimRefresh(ctx, model.RefreshToken{}, time.Now())
	require.Error(t, err)
	require.Error(t, store.LinkRefresh(ctx, model.RefreshToken{}, 0))
	require.Error(t, store.RevokeFamily(ctx, 1, 0, "", "logout", time.Now()))
	require.Empty(t, capture.statements)
}

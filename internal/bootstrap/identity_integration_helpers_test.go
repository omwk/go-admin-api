package bootstrap

import (
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/idgen"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

type writeFailureRedis struct {
	identity.Revocations
	fail bool
}

func (r *writeFailureRedis) Mark(ctx context.Context, key string, expires, now time.Time) error {
	if r.fail {
		return errors.New("injected Redis write failure")
	}
	return r.Revocations.Mark(ctx, key, expires, now)
}

type identityFixture struct {
	ctx      context.Context
	database *db.DB
	store    *authStore
	service  *identity.AuthService
	server   *miniredis.Miniredis
	redis    *authRedis
	writes   *writeFailureRedis
	now      time.Time
	password string
	user     model.User
	secret   []byte
}

func isolatedIdentityDB(t *testing.T) (*db.DB, context.Context) {
	t.Helper()
	dsn := testenv.Lookup("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	// 夹具要做建 schema、迁移、种子（含多次 bcrypt）；`-race` 下慢一个数量级，
	// 60s 会在 CI 上被击穿。这里放宽到 5 分钟，失败时仍会给出明确的 deadline 错误。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	ctx = logger.WithTraceID(ctx, "identity-integration-trace")
	cfg := config.DB{DSN: dsn, MaxOpen: 10, MaxIdle: 4, ConnMaxLifetime: time.Minute, TxnTimeout: testenv.Timeouts(t).DB.TxnTimeout, MigrateTimeout: testenv.Timeouts(t).DB.MigrateTimeout}
	admin, err := db.Open(ctx, cfg)
	require.NoError(t, err)
	id, err := idgen.NewULID()
	require.NoError(t, err)
	schema := "t13_" + strings.ToLower(id)
	require.NoError(t, admin.Exec(ctx, "CREATE SCHEMA "+schema))
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		require.NoError(t, admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"))
		require.NoError(t, admin.Close())
	})
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", schema+",public")
		parsed.RawQuery = query.Encode()
		cfg.DSN = parsed.String()
	} else {
		cfg.DSN += " search_path=" + schema + ",public"
	}
	database, err := db.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	return database, ctx
}

func newIdentityFixture(t *testing.T) *identityFixture {
	t.Helper()
	database, ctx := isolatedIdentityDB(t)
	f := &identityFixture{database: database, ctx: ctx, now: time.Unix(1_800_000_000, 0).UTC()}
	f.server = miniredis.RunT(t)
	f.server.SetTime(f.now)
	cfg := config.Redis{Addr: f.server.Addr(), PoolSize: 10, MinIdleConns: 1}
	shared, err := cache.Open(ctx, cfg, testenv.Timeouts(t).Cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, shared.Close()) })
	f.redis, err = newAuthRedis(ctx, shared, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.redis.markers.Close()) })
	f.store = newAuthStore(database)
	f.writes = &writeFailureRedis{Revocations: f.redis}
	f.secret = make([]byte, 32)
	_, err = rand.Read(f.secret)
	require.NoError(t, err)
	f.useStore(t, f.store)
	f.addUser(t)
	return f
}

func (f *identityFixture) useStore(t *testing.T, store repo.Auth) {
	t.Helper()
	var err error
	f.service, err = identity.NewAuthService(identity.AuthDependencies{
		Store: store, Redis: f.writes, Secret: f.secret, Now: func() time.Time { return f.now },
		Audit: func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, f.database, event) },
	})
	require.NoError(t, err)
}

func (f *identityFixture) addUser(t *testing.T) {
	t.Helper()
	var err error
	f.password, err = refresh.Generate()
	require.NoError(t, err)
	hash, err := pwd.Hash(f.password)
	require.NoError(t, err)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	dept := model.Department{TenantID: 1, Name: "root", Path: "1", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	f.user = model.User{TenantID: 1, Username: "operator", PasswordHash: hash, DepartmentID: dept.ID, Status: "active"}
	require.NoError(t, q.Create(&f.user).Error)
}

func (f *identityFixture) login(t *testing.T) identity.Tokens {
	t.Helper()
	tokens, err := f.service.Login(f.ctx, 1, f.user.Username, f.password, identity.RequestMeta{IP: "192.0.2.1"})
	require.NoError(t, err)
	return tokens
}

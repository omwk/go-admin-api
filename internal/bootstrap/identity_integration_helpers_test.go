package bootstrap

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
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
	scheduleIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	ctx = logger.WithTraceID(ctx, "identity-integration-trace")
	cfg := config.DB{DSN: dsn, MaxOpen: 10, MaxIdle: 4, ConnMaxLifetime: time.Minute, TxnTimeout: testenv.Timeouts(t).DB.TxnTimeout, MigrateTimeout: testenv.Timeouts(t).DB.MigrateTimeout}
	cfg.DSN = testenv.Database(ctx, t, dsn, func(ctx context.Context, isolated string) error {
		migrationConfig := cfg
		migrationConfig.DSN = isolated
		database, err := db.Open(ctx, migrationConfig)
		if err != nil {
			return err
		}
		_, err = database.Migrate(ctx, "up")
		return errors.Join(err, database.Close())
	})
	database, err := db.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
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

// Only immutable password material is shared; every test still inserts its own
// user and exercises the real password verification and seed paths.
type fixturePasswordMaterial struct{ password, hash string }

var fixturePassword = sync.OnceValues(func() (fixturePasswordMaterial, error) {
	password, err := refresh.Generate()
	if err != nil {
		return fixturePasswordMaterial{}, err
	}
	hash, err := pwd.Hash(password)
	return fixturePasswordMaterial{password: password, hash: hash}, err
})

func (f *identityFixture) addUser(t *testing.T) {
	t.Helper()
	material, err := fixturePassword()
	require.NoError(t, err)
	f.password = material.password
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	dept := model.Department{TenantID: 1, Name: "root", Path: "1", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	f.user = model.User{TenantID: 1, Username: "operator", PasswordHash: material.hash, DepartmentID: dept.ID, Status: "active"}
	require.NoError(t, q.Create(&f.user).Error)
}

func (f *identityFixture) login(t *testing.T) identity.Tokens {
	t.Helper()
	tokens, err := f.service.Login(f.ctx, 1, f.user.Username, f.password, identity.RequestMeta{IP: "192.0.2.1"})
	require.NoError(t, err)
	return tokens
}

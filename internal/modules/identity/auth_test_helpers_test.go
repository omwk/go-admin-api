package identity

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

type fakeTxKey struct{}

type memoryAuth struct {
	mu         sync.Mutex
	user       model.User
	tokens     map[int64]model.RefreshToken
	audits     []audit.Event
	nextID     int64
	failInsert bool
}

func (m *memoryAuth) Transaction(ctx context.Context, fn func(context.Context) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	user := m.user
	tokens := make(map[int64]model.RefreshToken, len(m.tokens))
	for k, v := range m.tokens {
		tokens[k] = v
	}
	auditCount := len(m.audits)
	if err := fn(context.WithValue(ctx, fakeTxKey{}, true)); err != nil {
		m.user, m.tokens, m.audits = user, tokens, m.audits[:auditCount]
		return err
	}
	return nil
}

func (m *memoryAuth) read(ctx context.Context) func() {
	if ctx.Value(fakeTxKey{}) == true {
		return func() {}
	}
	m.mu.Lock()
	return m.mu.Unlock
}

func (m *memoryAuth) UserForLogin(ctx context.Context, tenant int64, name string) (model.User, bool, error) {
	defer m.read(ctx)()
	return m.user, m.user.TenantID == tenant && m.user.Username == name, nil
}

func (m *memoryAuth) SaveLogin(_ context.Context, user model.User) error { m.user = user; return nil }

func (m *memoryAuth) RefreshByHash(ctx context.Context, hash string) (model.RefreshToken, bool, error) {
	defer m.read(ctx)()
	for _, token := range m.tokens {
		if token.TokenHash == hash {
			return token, true, nil
		}
	}
	return model.RefreshToken{}, false, nil
}

func (m *memoryAuth) LockSession(context.Context, int64, string) error { return nil }

func (m *memoryAuth) Family(ctx context.Context, tenant int64, user int64, sid string) (repo.Family, error) {
	defer m.read(ctx)()
	family := repo.Family{}
	for _, token := range m.tokens {
		if token.TenantID == tenant && token.UserID == user && token.SessionID == sid {
			family.Tokens = append(family.Tokens, token)
		}
	}
	return family, nil
}

func (m *memoryAuth) SessionRevoked(ctx context.Context, tenant, user int64, sid string) (bool, error) {
	defer m.read(ctx)()
	found := false
	for _, row := range m.tokens {
		if row.TenantID != tenant || row.UserID != user || row.SessionID != sid {
			continue
		}
		if row.RevokedAt == nil {
			found = true
		}
		if row.RevokedReason != nil {
			switch *row.RevokedReason {
			case "logout", "kick", "replay":
				return true, nil
			}
		}
	}
	return !found, nil
}

func (m *memoryAuth) InsertRefresh(_ context.Context, token *model.RefreshToken) error {
	if m.failInsert {
		return errors.New("insert failed")
	}
	m.nextID++
	token.ID = m.nextID
	m.tokens[token.ID] = *token
	return nil
}

func (m *memoryAuth) ClaimRefresh(_ context.Context, old model.RefreshToken, now time.Time) (bool, error) {
	row := m.tokens[old.ID]
	if row.RevokedAt != nil {
		return false, nil
	}
	reason := "rotated"
	row.RevokedAt, row.RevokedReason = &now, &reason
	m.tokens[old.ID] = row
	return true, nil
}

func (m *memoryAuth) LinkRefresh(_ context.Context, old model.RefreshToken, id int64) error {
	row := m.tokens[old.ID]
	row.ReplacedByTokenID = &id
	m.tokens[old.ID] = row
	return nil
}

func (m *memoryAuth) RevokeFamily(_ context.Context, tenant int64, user int64, sid string, reason string, now time.Time) error {
	for id, token := range m.tokens {
		if token.TenantID == tenant && token.UserID == user && token.SessionID == sid {
			if token.RevokedAt == nil {
				token.RevokedAt = &now
			}
			token.RevokedReason = &reason
			m.tokens[id] = token
		}
	}
	return nil
}

type marker struct{ expires time.Time }
type memoryRedis struct {
	mu          sync.Mutex
	now         func() time.Time
	values      map[string]marker
	down        bool
	failMark    bool
	limited     bool
	afterCommit func()
}

func (r *memoryRedis) available(ctx context.Context) error {
	if ctx.Value(fakeTxKey{}) == true {
		return errors.New("Redis IO inside database transaction")
	}
	if r.down {
		return errors.New("Redis unavailable")
	}
	return nil
}

func (r *memoryRedis) Ping(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.available(ctx)
}
func (r *memoryRedis) AllowLogin(ctx context.Context, _ string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.limited, r.available(ctx)
}
func (r *memoryRedis) Revoked(ctx context.Context, key string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.available(ctx); err != nil {
		return false, err
	}
	value, found := r.values[key]
	return found && value.expires.After(r.now()), nil
}
func (r *memoryRedis) Mark(ctx context.Context, key string, expires time.Time, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.available(ctx); err != nil {
		return err
	}
	if r.failMark {
		return errors.New("Redis write failed")
	}
	if expires.After(now) && expires.After(r.values[key].expires) {
		r.values[key] = marker{expires: expires}
	}
	if r.afterCommit != nil {
		r.afterCommit()
	}
	return nil
}

type authFixture struct {
	service  *AuthService
	store    *memoryAuth
	redis    *memoryRedis
	ctx      context.Context
	now      time.Time
	password string
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	password, err := refresh.Generate()
	require.NoError(t, err)
	hash, err := pwd.Hash(password)
	require.NoError(t, err)
	f := &authFixture{now: time.Unix(1_800_000_000, 0).UTC(), password: password}
	f.ctx = logger.WithTraceID(context.Background(), "auth-test-trace")
	f.store = &memoryAuth{user: model.User{ID: 7, TenantID: 1, Username: "operator", Status: "active", PasswordHash: hash}, tokens: make(map[int64]model.RefreshToken)}
	f.redis = &memoryRedis{now: func() time.Time { return f.now }, values: make(map[string]marker)}
	secret := make([]byte, 32)
	_, err = rand.Read(secret)
	require.NoError(t, err)
	f.service, err = NewAuthService(AuthDependencies{Store: f.store, Redis: f.redis, Secret: secret, Now: func() time.Time { return f.now }, Audit: func(_ context.Context, event audit.Event) error {
		f.store.audits = append(f.store.audits, event)
		return nil
	}})
	require.NoError(t, err)
	return f
}

func (f *authFixture) login(t *testing.T) Tokens {
	t.Helper()
	tokens, err := f.service.Login(f.ctx, 1, "operator", f.password, RequestMeta{})
	require.NoError(t, err)
	return tokens
}

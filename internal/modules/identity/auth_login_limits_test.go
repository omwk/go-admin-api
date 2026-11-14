package identity

import (
	"context"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/stretchr/testify/require"
)

func (m *memoryAuth) TenantForLogin(_ context.Context, code string, id int64) (model.Tenant, bool, error) {
	found := code == "default" || code == "" && id == m.user.TenantID
	return model.Tenant{ID: m.user.TenantID, Code: "default", Status: "active"}, found, nil
}

func (r *memoryRedis) AllowTenantLogin(ctx context.Context, _ string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return true, r.available(ctx)
}

func (r *memoryRedis) RecordLoginFailure(ctx context.Context, _, _ string, _ time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.available(ctx)
}

func (r *memoryRedis) ClearLoginFailures(ctx context.Context, _, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.available(ctx)
}

func TestLoginUnknownTenantPerformsDummyBcrypt(t *testing.T) {
	// BE-IDN-001 §5.3: do real dummy bcrypt work without attempting a foreign user's lookup.
	f := newAuthFixture(t)
	comparisons := 0
	f.service.verify = func(hash, password string) error {
		comparisons++
		require.Equal(t, f.service.dummyHash, hash)
		return pwd.Verify(hash, password)
	}
	_, err := f.service.LoginByCode(f.ctx, "absent", "operator", f.password, RequestMeta{})
	require.Equal(t, 401, errs.From(err).HTTPStatus)
	require.Equal(t, "企业代码、账号或密码错误", errs.From(err).Message)
	require.Equal(t, 1, comparisons)
	require.Empty(t, f.store.tokens)
}

type loginTenantGate struct {
	*memoryAuth
	checks    int
	suspended bool
}

func (g *loginTenantGate) CheckTenant(ctx context.Context, _ int64) error {
	g.checks++
	if ctx.Value(fakeTxKey{}) != true {
		return errs.New(errs.ErrInternal, 503, "tenant gate requires the login transaction")
	}
	if g.suspended {
		return errs.New(errs.ErrTenantSuspended, 403, "租户已停用")
	}
	return nil
}

func TestLoginTenantSuspendedDuringBcryptCannotIssueSession(t *testing.T) {
	// BE-IDN-001: the password check is outside the transaction; the status gate is inside.
	f := newAuthFixture(t)
	gate := &loginTenantGate{memoryAuth: f.store}
	f.service.store = gate
	f.service.verify = func(hash, password string) error {
		require.Zero(t, gate.checks, "tenant status must not be checked before bcrypt")
		err := pwd.Verify(hash, password)
		gate.suspended = true
		return err
	}
	_, err := f.service.LoginByCode(f.ctx, "default", "operator", f.password, RequestMeta{})
	require.Equal(t, 403, errs.From(err).HTTPStatus)
	require.Equal(t, errs.ErrTenantSuspended, errs.From(err).Code)
	require.Equal(t, 1, gate.checks)
	require.Empty(t, f.store.tokens)
	gate.checks = 0
	_, err = f.service.LoginByCode(f.ctx, "default", "operator", f.password+"wrong", RequestMeta{})
	require.Equal(t, 401, errs.From(err).HTTPStatus)
	require.Zero(t, gate.checks, "bad passwords must not reach the tenant status gate")
}

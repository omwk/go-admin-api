package identity

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func (m *memoryAuth) UserForReauth(ctx context.Context, tenant, user int64) (model.User, bool, error) {
	defer m.read(ctx)()
	return m.user, m.user.TenantID == tenant && m.user.ID == user, nil
}

func (m *memoryAuth) SaveReauth(ctx context.Context, tenant, user int64, sid string, at time.Time) error {
	defer m.read(ctx)()
	for id, token := range m.tokens {
		if token.TenantID == tenant && token.UserID == user && token.SessionID == sid {
			token.ReauthAt = &at
			m.tokens[id] = token
		}
	}
	return nil
}

func (m *memoryAuth) ReauthAt(ctx context.Context, tenant, user int64, sid string, now time.Time) (*time.Time, error) {
	defer m.read(ctx)()
	if m.user.ID != user || m.user.TenantID != tenant || m.user.Status != "active" {
		return nil, nil
	}
	var latest *time.Time
	for _, row := range m.tokens {
		if row.TenantID != tenant || row.UserID != user || row.SessionID != sid {
			continue
		}
		if row.RevokedReason != nil && (*row.RevokedReason == "logout" || *row.RevokedReason == "kick" || *row.RevokedReason == "replay") {
			return nil, nil
		}
		if row.RevokedAt == nil && row.ExpiresAt.After(now) && row.ReauthAt != nil && (latest == nil || row.ReauthAt.After(*latest)) {
			latest = row.ReauthAt
		}
	}
	return latest, nil
}

func reauthClaims(t *testing.T, f *authFixture, token Tokens) jwt.Claims {
	t.Helper()
	claims, err := f.service.Authenticate(f.ctx, token.AccessToken)
	require.NoError(t, err)
	return claims
}

func TestAuthReauthSessionBindingAndStrictWindow(t *testing.T) {
	// REQ-ORD-006: a fresh login never substitutes for explicit password reauthentication.
	f := newAuthFixture(t)
	first, second := f.login(t), f.login(t)
	claims := reauthClaims(t, f, first)
	other := reauthClaims(t, f, second)
	user, err := strconv.ParseInt(claims.Subject, 10, 64)
	require.NoError(t, err)
	denied := func(sid string) {
		err := f.service.RequireRecent(f.ctx, claims.TenantID, user, sid)
		require.Equal(t, 403, errs.From(err).HTTPStatus)
		require.Equal(t, errs.ErrDataScopeDenied, errs.From(err).Code)
	}
	denied(claims.SessionID)
	result, err := f.service.Reauthenticate(f.ctx, claims, f.password, RequestMeta{})
	require.NoError(t, err)
	require.Equal(t, f.now, result.ReauthAt)
	require.Equal(t, f.now.Add(5*time.Minute), result.ExpiresAt)
	require.NoError(t, f.service.RequireRecent(f.ctx, claims.TenantID, user, claims.SessionID))
	denied(other.SessionID)
	denied("missing-session")
	f.now = f.now.Add(5*time.Minute - time.Nanosecond)
	require.NoError(t, f.service.RequireRecent(f.ctx, claims.TenantID, user, claims.SessionID))
	f.now = f.now.Add(time.Nanosecond)
	denied(claims.SessionID)
	f.now = result.ReauthAt.Add(-time.Nanosecond)
	denied(claims.SessionID)
}

func TestAuthReauthRefreshPreservesTimestampAndLogoutRejects(t *testing.T) {
	f := newAuthFixture(t)
	first := f.login(t)
	claims := reauthClaims(t, f, first)
	result, err := f.service.Reauthenticate(f.ctx, claims, f.password, RequestMeta{})
	require.NoError(t, err)
	f.now = f.now.Add(4 * time.Minute)
	rotated, err := f.service.Refresh(f.ctx, first.RefreshToken, RequestMeta{})
	require.NoError(t, err)
	row, found, err := f.store.RefreshByHash(f.ctx, refresh.Hash(rotated.RefreshToken))
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, row.ReauthAt)
	require.Equal(t, result.ReauthAt, *row.ReauthAt)
	require.NoError(t, f.service.RequireRecent(f.ctx, 1, f.store.user.ID, claims.SessionID))
	f.now = f.now.Add(time.Minute)
	require.Equal(t, 20108, errs.From(f.service.RequireRecent(f.ctx, 1, f.store.user.ID, claims.SessionID)).Code)
	f.now = f.now.Add(-time.Minute)
	require.NoError(t, f.service.Logout(f.ctx, reauthClaims(t, f, rotated)))
	require.Error(t, f.service.RequireRecent(f.ctx, 1, f.store.user.ID, claims.SessionID))
	_, err = f.service.Reauthenticate(f.ctx, claims, f.password, RequestMeta{})
	require.Equal(t, 20103, errs.From(err).Code)
}

func TestAuthReauthWrongPasswordUsesLoginProtectionAndSafeAudit(t *testing.T) {
	f := newAuthFixture(t)
	issued := f.login(t)
	claims := reauthClaims(t, f, issued)
	wrong, err := refresh.Generate()
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		_, err = f.service.Reauthenticate(f.ctx, claims, wrong, RequestMeta{})
		want := 20101
		if i == 4 {
			want = 20104
		}
		require.Equal(t, want, errs.From(err).Code)
	}
	require.Equal(t, "locked", f.store.user.Status)
	for _, row := range f.store.tokens {
		require.Nil(t, row.ReauthAt)
	}
	_, err = f.service.Reauthenticate(f.ctx, claims, f.password, RequestMeta{})
	require.Equal(t, 20104, errs.From(err).Code)
	encoded, err := json.Marshal(f.store.audits)
	require.NoError(t, err)
	for _, secret := range []string{f.password, wrong, issued.AccessToken, issued.RefreshToken} {
		require.NotContains(t, string(encoded), secret)
	}
	require.Equal(t, "identity.reauth", f.store.audits[len(f.store.audits)-1].Action)
}

func TestAuthReauthBcryptOutsideTransactionAndCredentialChangeRejected(t *testing.T) {
	f := newAuthFixture(t)
	claims := reauthClaims(t, f, f.login(t))
	replacement, err := refresh.Generate()
	require.NoError(t, err)
	hash, err := pwd.Hash(replacement)
	require.NoError(t, err)
	f.service.verify = func(old, password string) error {
		unlocked := f.store.mu.TryLock()
		if unlocked {
			f.store.mu.Unlock()
		}
		require.True(t, unlocked)
		err := pwd.Verify(old, password)
		f.store.user.PasswordHash = hash
		return err
	}
	_, err = f.service.Reauthenticate(f.ctx, claims, f.password, RequestMeta{})
	require.Equal(t, 20101, errs.From(err).Code)
	for _, row := range f.store.tokens {
		require.Nil(t, row.ReauthAt)
	}
}

func TestAuthReauthAuditAndRedisFailuresDoNotGrantAccess(t *testing.T) {
	f := newAuthFixture(t)
	claims := reauthClaims(t, f, f.login(t))
	f.service.audit = func(context.Context, audit.Event) error { return errors.New("audit unavailable") }
	_, err := f.service.Reauthenticate(f.ctx, claims, f.password, RequestMeta{})
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	for _, row := range f.store.tokens {
		require.Nil(t, row.ReauthAt)
	}
	f.redis.down = true
	_, err = f.service.Reauthenticate(f.ctx, claims, f.password, RequestMeta{})
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	require.Equal(t, 503, errs.From(f.service.RequireRecent(f.ctx, 1, f.store.user.ID, claims.SessionID)).HTTPStatus)
}

func TestAuthReauthRateLimitDoesNotCountPasswordFailure(t *testing.T) {
	f := newAuthFixture(t)
	claims := reauthClaims(t, f, f.login(t))
	f.redis.limited = true
	_, err := f.service.Reauthenticate(f.ctx, claims, f.password, RequestMeta{})
	require.Equal(t, 429, errs.From(err).HTTPStatus)
	require.Zero(t, f.store.user.FailedAttempts)
	for _, row := range f.store.tokens {
		require.Nil(t, row.ReauthAt)
	}
}

func TestAuthReauthDisabledAccountCannotReadPII(t *testing.T) {
	f := newAuthFixture(t)
	claims := reauthClaims(t, f, f.login(t))
	_, err := f.service.Reauthenticate(f.ctx, claims, f.password, RequestMeta{})
	require.NoError(t, err)
	f.store.user.Status = "disabled"
	err = f.service.RequireRecent(f.ctx, 1, f.store.user.ID, claims.SessionID)
	require.Equal(t, 20108, errs.From(err).Code)
	_, err = f.service.Reauthenticate(f.ctx, claims, f.password, RequestMeta{})
	require.Equal(t, 20101, errs.From(err).Code)
}

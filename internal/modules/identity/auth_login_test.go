package identity

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestLoginSuccessAndFailureAreAuditedWithoutCredentials(t *testing.T) {
	// REQ-IAM-001/009: successful and failed attempts commit sanitized audit records.
	f := newAuthFixture(t)
	wrong, err := refresh.Generate()
	require.NoError(t, err)
	_, err = f.service.Login(f.ctx, 1, "operator", wrong, RequestMeta{})
	require.Equal(t, 20101, errs.From(err).Code)
	issued := f.login(t)
	require.Zero(t, f.store.user.FailedAttempts)
	require.NotNil(t, f.store.user.LastLoginAt)
	require.Len(t, f.store.audits, 2)
	require.Equal(t, "failure", f.store.audits[0].Result)
	require.Equal(t, "success", f.store.audits[1].Result)
	encoded, err := json.Marshal(f.store.audits)
	require.NoError(t, err)
	for _, sensitive := range []string{f.password, wrong, issued.AccessToken, issued.RefreshToken} {
		require.False(t, strings.Contains(string(encoded), sensitive), "audit must not contain credentials")
	}
}

func TestUsernameLockIsCrossIPAndIPLimitDoesNotCountFailures(t *testing.T) {
	// REQ-IAM-001: 5 failures lock the username for 15 minutes, independent of IP.
	f := newAuthFixture(t)
	wrong, err := refresh.Generate()
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		_, err = f.service.Login(f.ctx, 1, "operator", wrong, RequestMeta{IP: "192.0.2." + string(rune('1'+i))})
		want := 20101
		if i == 4 {
			want = 20104
		}
		require.Equal(t, want, errs.From(err).Code)
	}
	require.Equal(t, 5, f.store.user.FailedAttempts)
	until := *f.store.user.LockedUntil
	_, err = f.service.Login(f.ctx, 1, "operator", f.password, RequestMeta{IP: "192.0.2.99"})
	require.Equal(t, 20104, errs.From(err).Code)
	require.Equal(t, until, *f.store.user.LockedUntil)
	f.now = f.now.Add(15 * time.Minute)
	f.login(t)
	require.Equal(t, "active", f.store.user.Status)
	require.Zero(t, f.store.user.FailedAttempts)
	f.redis.limited = true
	_, err = f.service.Login(f.ctx, 1, "operator", wrong, RequestMeta{})
	require.Equal(t, 429, errs.From(err).HTTPStatus)
	require.Zero(t, f.store.user.FailedAttempts)
}

func TestUnknownUsernameStillPerformsBcryptComparison(t *testing.T) {
	f := newAuthFixture(t)
	comparisons := 0
	f.service.verify = func(hash, password string) error {
		comparisons++
		cost, err := bcrypt.Cost([]byte(hash))
		require.NoError(t, err)
		require.Equal(t, 12, cost)
		return pwd.Verify(hash, password)
	}
	_, err := f.service.Login(f.ctx, 1, "absent", f.password, RequestMeta{})
	require.Equal(t, 20101, errs.From(err).Code)
	require.Equal(t, 1, comparisons)
	require.Zero(t, f.store.user.FailedAttempts)
	require.Len(t, f.store.audits, 1)
}

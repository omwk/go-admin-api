package identity

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestLoginBcryptDoesNotHoldTransaction(t *testing.T) {
	f := newAuthFixture(t)
	f.service.verify = func(hash, password string) error {
		unlocked := f.store.mu.TryLock()
		if unlocked {
			f.store.mu.Unlock()
		}
		require.True(t, unlocked, "bcrypt comparison must run outside the database transaction")
		return pwd.Verify(hash, password)
	}
	f.login(t)
}

func TestLoginRejectsCredentialsChangedDuringBcrypt(t *testing.T) {
	f := newAuthFixture(t)
	replacement, err := refresh.Generate()
	require.NoError(t, err)
	replacementHash, err := pwd.Hash(replacement)
	require.NoError(t, err)
	f.service.verify = func(hash, password string) error {
		err := pwd.Verify(hash, password)
		f.store.user.PasswordHash = replacementHash
		return err
	}
	_, err = f.service.Login(f.ctx, 1, "operator", f.password, RequestMeta{})
	require.Equal(t, 20101, errs.From(err).Code)
	require.Empty(t, f.store.tokens)
	require.Equal(t, "failure", f.store.audits[0].Result)
}

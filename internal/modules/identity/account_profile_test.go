package identity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

type profileAccountStub struct {
	repo.Account
	memory *memoryAuth
}

func (s *profileAccountStub) Transaction(ctx context.Context, run func(context.Context) error) error {
	return s.memory.Transaction(ctx, run)
}

func (s *profileAccountStub) LockAccount(context.Context, int64, int64) error { return nil }

func (s *profileAccountStub) UserForReauth(ctx context.Context, tenant, user int64) (model.User, bool, error) {
	return s.memory.UserForReauth(ctx, tenant, user)
}

func (s *profileAccountStub) UpdateProfile(_ context.Context, user model.User) error {
	s.memory.user = user
	return nil
}

func TestProfilePhoneAuditRollbackAndPostCommitInvalidation(t *testing.T) {
	// BE-IDN-003: audit failure rolls back the phone and never invalidates before commit.
	f := newAuthFixture(t)
	invalidated := false
	accounts := &AccountService{Store: &profileAccountStub{memory: f.store}, Auth: f.service,
		Invalidate: func(ctx context.Context, tenant int64, users []int64) bool {
			require.Nil(t, ctx.Value(fakeTxKey{}))
			require.EqualValues(t, 1, tenant)
			require.Equal(t, []int64{f.store.user.ID}, users)
			invalidated = true
			return true
		}}
	phone := "+1" + strings.Map(func(r rune) rune { return '0' + r%10 }, f.password)[:10]
	claims := jwt.Claims{TenantID: 1, Subject: "7"}
	f.service.audit = func(ctx context.Context, event audit.Event) error {
		require.Equal(t, true, ctx.Value(fakeTxKey{}))
		require.Equal(t, "system:user:profile", event.Action)
		return errors.New("injected audit failure")
	}
	_, pending, err := accounts.UpdateProfile(f.ctx, claims, ProfileInput{Phone: &phone})
	require.Error(t, err)
	require.False(t, pending)
	require.False(t, invalidated)
	require.Nil(t, f.store.user.Phone)
	f.service.audit = func(ctx context.Context, event audit.Event) error {
		require.Equal(t, true, ctx.Value(fakeTxKey{}))
		require.Nil(t, event.Before["phone"].(*string))
		require.NotNil(t, event.After["phone"].(*string))
		return nil
	}
	_, pending, err = accounts.UpdateProfile(f.ctx, claims, ProfileInput{Phone: &phone})
	require.NoError(t, err)
	require.True(t, pending)
	require.True(t, invalidated)
	require.NotNil(t, f.store.user.Phone)
}

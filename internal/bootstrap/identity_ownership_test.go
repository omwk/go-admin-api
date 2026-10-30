package bootstrap

import (
	"context"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestIdentityIntegrationRejectsForeignOwnerAndTenant(t *testing.T) {
	// REQ-IAM-010: verified claims still bind the family to both tenant and user.
	f := newIdentityFixture(t)
	issued := f.login(t)
	old, _, err := f.store.RefreshByHash(f.ctx, refresh.Hash(issued.RefreshToken))
	require.NoError(t, err)
	other := model.User{TenantID: 1, Username: "another-operator", PasswordHash: f.user.PasswordHash, DepartmentID: f.user.DepartmentID, Status: "active"}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Create(&other).Error)
	family, err := f.store.Family(f.ctx, 1, other.ID, old.SessionID)
	require.NoError(t, err)
	require.Empty(t, family.Tokens)
	family, err = f.store.Family(f.ctx, 2, f.user.ID, old.SessionID)
	require.NoError(t, err)
	require.Empty(t, family.Tokens)
	_, found, err := f.store.UserForLogin(f.ctx, 2, f.user.Username)
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, f.store.Transaction(f.ctx, func(ctx context.Context) error {
		if err := f.store.LockSession(ctx, 1, old.SessionID); err != nil {
			return err
		}
		foreign := old
		foreign.UserID = other.ID
		claimed, err := f.store.ClaimRefresh(ctx, foreign, f.now)
		require.False(t, claimed)
		if err != nil {
			return err
		}
		return f.store.RevokeFamily(ctx, 1, other.ID, old.SessionID, "logout", f.now)
	}))
	_, err = f.service.Authenticate(f.ctx, issued.AccessToken)
	require.NoError(t, err)
	foreignAccess, _, err := jwt.Issue(f.secret, 1, other.ID, old.SessionID, f.now)
	require.NoError(t, err)
	_, err = f.service.Authenticate(f.ctx, foreignAccess)
	require.Equal(t, 20103, errs.From(err).Code)
}

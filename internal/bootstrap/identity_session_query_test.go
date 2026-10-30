package bootstrap

import (
	"context"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/session"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

func TestAuthenticateIntegrationUsesOneExistenceQuery(t *testing.T) {
	// REQ-IAM-010: authentication never materializes the refresh history.
	f := newIdentityFixture(t)
	issued := f.login(t)
	claims, err := f.service.Authenticate(f.ctx, issued.AccessToken)
	require.NoError(t, err)
	addRotatedHistory(t, f, claims.SessionID, 50)
	capture := &sqlCapture{Interface: gormlog.Discard}
	query := f.store.query
	f.store.query = func(ctx context.Context) (*gorm.DB, context.CancelFunc) {
		q, cancel := query(ctx)
		return q.Session(&gorm.Session{Logger: capture}), cancel
	}
	_, err = f.service.Authenticate(f.ctx, issued.AccessToken)
	require.NoError(t, err)
	assertExistenceQuery(t, capture)
	require.NoError(t, f.store.Transaction(f.ctx, func(ctx context.Context) error {
		return f.store.RevokeFamily(ctx, claims.TenantID, f.user.ID, claims.SessionID, "logout", f.now)
	}))
	f.server.FlushAll()
	capture.statements = nil
	_, err = f.service.Authenticate(f.ctx, issued.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	assertExistenceQuery(t, capture)
	sid, err := session.NewID()
	require.NoError(t, err)
	unknown, _, err := jwt.Issue(f.secret, 1, f.user.ID, sid, f.now)
	require.NoError(t, err)
	capture.statements = nil
	_, err = f.service.Authenticate(f.ctx, unknown)
	require.Equal(t, 20103, errs.From(err).Code)
	assertExistenceQuery(t, capture)
}

func assertExistenceQuery(t *testing.T, capture *sqlCapture) {
	t.Helper()
	require.Len(t, capture.statements, 1)
	require.Contains(t, capture.statements[0], "EXISTS")
	require.NotContains(t, capture.statements[0], "token_hash")
	require.NotContains(t, capture.statements[0], "SELECT *")
}

func addRotatedHistory(t *testing.T, f *identityFixture, sid string, count int) {
	t.Helper()
	rows := make([]model.RefreshToken, 0, count)
	reason := "rotated"
	for i := 0; i < count; i++ {
		raw, err := refresh.Generate()
		require.NoError(t, err)
		rows = append(rows, model.RefreshToken{TenantID: 1, UserID: f.user.ID, SessionID: sid, TokenHash: refresh.Hash(raw), ExpiresAt: f.now.Add(refresh.TTL), RevokedAt: &f.now, RevokedReason: &reason, CreatedAt: f.now})
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.CreateInBatches(rows, 50).Error)
}

func TestSessionRevokedDryRunIsBoundedAndRejectsMissingIdentity(t *testing.T) {
	store, capture := dryAuthStore(t)
	_, err := store.SessionRevoked(context.Background(), 17, 29, "known-session")
	require.NoError(t, err)
	assertExistenceQuery(t, capture)
	require.Contains(t, capture.statements[0], "tenant_id = 17 AND user_id = 29 AND session_id = 'known-session'")
	require.Contains(t, capture.statements[0], "revoked_at IS NULL")
	require.Contains(t, capture.statements[0], "revoked_reason IN ('logout', 'kick', 'replay')")
	capture.statements = nil
	_, err = store.SessionRevoked(context.Background(), 0, 0, "")
	require.Error(t, err)
	require.Empty(t, capture.statements)
}

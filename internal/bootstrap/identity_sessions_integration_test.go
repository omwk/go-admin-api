package bootstrap

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/session"
	"github.com/stretchr/testify/require"
)

func TestSessionsIntegrationListKickRepeatUnknownAndRedisFlush(t *testing.T) {
	// REQ-IAM-010: full history is persistent and one device never revokes another.
	f := newAdminFixture(t)
	first, second := f.login(t), f.login(t)
	claims, err := f.identityFixture.service.Authenticate(f.ctx, first.AccessToken)
	require.NoError(t, err)
	rotated, err := f.identityFixture.service.Refresh(f.ctx, first.RefreshToken, identity.RequestMeta{})
	require.NoError(t, err)
	response, envelope := adminRequest(t, f, "GET", "/sessions", nil, "")
	require.Equal(t, 200, response.Code)
	var page identity.AdminPage[repo.Session]
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 3, page.Total)
	require.Len(t, page.List, 3)
	require.NotContains(t, response.Body.String(), "last_seen")
	for i := 0; i < 2; i++ {
		response, _ = adminRequest(t, f, "DELETE", "/sessions/"+claims.SessionID, nil, "")
		require.Equal(t, 204, response.Code)
	}
	response, _ = adminRequest(t, f, "DELETE", "/sessions/unknown", nil, "")
	require.Equal(t, 404, response.Code)
	_, err = f.identityFixture.service.Authenticate(f.ctx, rotated.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	rejected, _ := profileRequest(t, f.router, "/api/v1/auth/me", rotated.AccessToken)
	require.Equal(t, 401, rejected.Code)
	_, err = f.identityFixture.service.Authenticate(f.ctx, second.AccessToken)
	require.NoError(t, err)
	f.server.FlushAll()
	_, err = f.identityFixture.service.Authenticate(f.ctx, rotated.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.identityFixture.service.Refresh(f.ctx, rotated.RefreshToken, identity.RequestMeta{})
	require.Equal(t, 20103, errs.From(err).Code)
	response, envelope = adminRequest(t, f, "GET", "/sessions", nil, "")
	require.Equal(t, 200, response.Code)
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 2, page.Total)
}

func TestSessionsIntegrationFailureExpiredHistoryAndOwnerScope(t *testing.T) {
	f := newAdminFixture(t)
	issued := f.login(t)
	claims, err := f.identityFixture.service.Authenticate(f.ctx, issued.AccessToken)
	require.NoError(t, err)
	f.writes.fail = true
	response, _ := adminRequest(t, f, "DELETE", "/sessions/"+claims.SessionID, nil, "")
	require.Equal(t, 503, response.Code)
	f.writes.fail = false
	f.server.FlushAll()
	_, err = f.identityFixture.service.Authenticate(f.ctx, issued.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	sid, err := session.NewID()
	require.NoError(t, err)
	raw, err := refresh.Generate()
	require.NoError(t, err)
	row := model.RefreshToken{TenantID: 1, UserID: f.user.ID, SessionID: sid, TokenHash: refresh.Hash(raw), ExpiresAt: f.now.Add(-time.Hour), CreatedAt: f.now.Add(-2 * time.Hour)}
	require.NoError(t, q.Create(&row).Error)
	response, envelope := adminRequest(t, f, "GET", "/sessions", nil, "")
	require.Equal(t, 200, response.Code)
	require.NotContains(t, string(envelope.Data), sid)
	response, _ = adminRequest(t, f, "DELETE", "/sessions/"+sid, nil, "")
	require.Equal(t, 204, response.Code)
	response, _ = adminRequest(t, f, "DELETE", "/sessions/"+sid, nil, "")
	require.Equal(t, 204, response.Code)
	foreignDept := model.Department{TenantID: 2, Name: "foreign-session", Path: "2", Status: "active"}
	require.NoError(t, q.Create(&foreignDept).Error)
	foreignUser := model.User{TenantID: 2, Username: "foreign-session", PasswordHash: f.user.PasswordHash, DepartmentID: foreignDept.ID, Status: "active"}
	require.NoError(t, q.Create(&foreignUser).Error)
	foreignSID, err := session.NewID()
	require.NoError(t, err)
	foreignRaw, err := refresh.Generate()
	require.NoError(t, err)
	foreign := model.RefreshToken{TenantID: 2, UserID: foreignUser.ID, SessionID: foreignSID, TokenHash: refresh.Hash(foreignRaw), ExpiresAt: f.now.Add(time.Hour), CreatedAt: f.now}
	require.NoError(t, q.Create(&foreign).Error)
	response, _ = adminRequest(t, f, "DELETE", "/sessions/"+foreignSID, nil, "")
	require.Equal(t, 404, response.Code)
	family, err := f.identityFixture.store.Family(f.ctx, 2, foreignUser.ID, foreignSID)
	require.NoError(t, err)
	require.Nil(t, family.Tokens[0].RevokedAt)
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id = ? AND code = ?", 1, identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	response, _ = adminRequest(t, f, "DELETE", "/sessions/"+sid, nil, "")
	require.Equal(t, 404, response.Code)
	response, envelope = adminRequest(t, f, "GET", "/sessions", nil, "")
	require.Equal(t, 200, response.Code)
	var page identity.AdminPage[repo.Session]
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 1, page.Total)
}

type heldRefreshLock struct {
	repo.Auth
	acquired, release chan struct{}
}

type attemptedSessionLock struct {
	repo.Auth
	attempted chan struct{}
}

func (s *attemptedSessionLock) LockSession(ctx context.Context, tenant int64, sid string) error {
	close(s.attempted)
	return s.Auth.LockSession(ctx, tenant, sid)
}

func (s *heldRefreshLock) LockSession(ctx context.Context, tenant int64, sid string) error {
	if err := s.Auth.LockSession(ctx, tenant, sid); err != nil {
		return err
	}
	close(s.acquired)
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestSessionsIntegrationKickAndRefreshUseSameLock(t *testing.T) {
	f := newAdminFixture(t)
	issued := f.login(t)
	claims, err := f.identityFixture.service.Authenticate(f.ctx, issued.AccessToken)
	require.NoError(t, err)
	locked := &heldRefreshLock{Auth: f.identityFixture.store, acquired: make(chan struct{}), release: make(chan struct{})}
	f.identityFixture.useStore(t, locked)
	refreshing := f.identityFixture.service
	attempted := &attemptedSessionLock{Auth: f.identityFixture.store, attempted: make(chan struct{})}
	f.identityFixture.useStore(t, attempted)
	f.service.UseSessions(&sessionStore{admin: f.store}, f.identityFixture.service)
	refreshResult := make(chan error, 1)
	var next identity.Tokens
	go func() {
		var err error
		next, err = refreshing.Refresh(f.ctx, issued.RefreshToken, identity.RequestMeta{})
		refreshResult <- err
	}()
	select {
	case <-locked.acquired:
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	kickResult := make(chan error, 1)
	go func() { kickResult <- f.service.Kick(f.ctx, f.actor, claims.SessionID) }()
	select {
	case <-attempted.attempted:
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	close(locked.release)
	require.NoError(t, <-refreshResult)
	require.NoError(t, <-kickResult)
	f.server.FlushAll()
	_, err = f.identityFixture.service.Authenticate(f.ctx, next.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	family, err := f.identityFixture.store.Family(f.ctx, 1, f.user.ID, claims.SessionID)
	require.NoError(t, err)
	for _, row := range family.Tokens {
		require.NotNil(t, row.RevokedReason)
		require.Equal(t, "kick", *row.RevokedReason)
	}
}

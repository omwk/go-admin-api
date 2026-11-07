package bootstrap

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestAuthReauthIntegrationHTTPAndSessionWindow(t *testing.T) {
	// REQ-ORD-006: recent password verification belongs to one live session family.
	f := newIdentityFixture(t)
	first, other := f.login(t), f.login(t)
	claims, err := f.service.Authenticate(f.ctx, first.AccessToken)
	require.NoError(t, err)
	otherClaims, err := f.service.Authenticate(f.ctx, other.AccessToken)
	require.NoError(t, err)
	router := identityRouter(t, f)
	response, _ := authRequest(t, router, "/api/v1/auth/reauth", map[string]string{"password": f.password}, "")
	require.Equal(t, 401, response.Code)
	response, _ = authRequest(t, router, "/api/v1/auth/reauth", map[string]string{}, first.AccessToken)
	require.Equal(t, 400, response.Code)
	require.Equal(t, 20108, errs.From(f.service.RequireRecent(f.ctx, 1, f.user.ID, claims.SessionID)).Code)
	response, _ = authRequest(t, router, "/api/v1/auth/reauth", map[string]string{"password": f.password}, first.AccessToken)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var envelope struct {
		Data identity.ReauthResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, f.now, envelope.Data.ReauthAt)
	require.Equal(t, f.now.Add(5*time.Minute), envelope.Data.ExpiresAt)
	require.NoError(t, f.service.RequireRecent(f.ctx, 1, f.user.ID, claims.SessionID))
	require.Equal(t, 20108, errs.From(f.service.RequireRecent(f.ctx, 1, f.user.ID, otherClaims.SessionID)).Code)
	require.Equal(t, 20108, errs.From(f.service.RequireRecent(f.ctx, 2, f.user.ID, claims.SessionID)).Code)
	f.now = f.now.Add(4 * time.Minute)
	next, err := f.service.Refresh(f.ctx, first.RefreshToken, identity.RequestMeta{})
	require.NoError(t, err)
	row, found, err := f.store.RefreshByHash(f.ctx, refresh.Hash(next.RefreshToken))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, envelope.Data.ReauthAt, row.ReauthAt.UTC())
	f.now = envelope.Data.ReauthAt.Add(5 * time.Minute)
	require.Equal(t, 20108, errs.From(f.service.RequireRecent(f.ctx, 1, f.user.ID, claims.SessionID)).Code)
	f.now = envelope.Data.ReauthAt.Add(-time.Second)
	require.Equal(t, 20108, errs.From(f.service.RequireRecent(f.ctx, 1, f.user.ID, claims.SessionID)).Code)
}

func TestAuthReauthIntegrationFailureProtectionAuditAndRevocation(t *testing.T) {
	f := newIdentityFixture(t)
	issued := f.login(t)
	claims, err := f.service.Authenticate(f.ctx, issued.AccessToken)
	require.NoError(t, err)
	router := identityRouter(t, f)
	wrong, err := refresh.Generate()
	require.NoError(t, err)
	response, _ := authRequest(t, router, "/api/v1/auth/reauth", map[string]string{"password": wrong}, issued.AccessToken)
	require.Equal(t, 401, response.Code)
	response, _ = authRequest(t, router, "/api/v1/auth/reauth", map[string]string{"password": f.password}, issued.AccessToken)
	require.Equal(t, 200, response.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var audits []string
	require.NoError(t, q.Raw("SELECT row_to_json(a)::text FROM audit_log a WHERE action='identity.reauth' ORDER BY id").Scan(&audits).Error)
	require.Len(t, audits, 2)
	for _, encoded := range audits {
		for _, sensitive := range []string{f.password, wrong, issued.AccessToken, issued.RefreshToken} {
			require.NotContains(t, encoded, sensitive)
		}
	}
	require.Contains(t, audits[0], "failure")
	require.Contains(t, audits[1], "success")
	require.NoError(t, f.service.Logout(f.ctx, claims))
	f.server.FlushAll()
	require.Equal(t, 20108, errs.From(f.service.RequireRecent(f.ctx, 1, f.user.ID, claims.SessionID)).Code)
	response, _ = authRequest(t, router, "/api/v1/auth/reauth", map[string]string{"password": f.password}, issued.AccessToken)
	require.Equal(t, 401, response.Code)
}

func TestAuthReauthIntegrationAuditFailureRollsBackTimestamp(t *testing.T) {
	f := newIdentityFixture(t)
	issued := f.login(t)
	claims, err := f.service.Authenticate(f.ctx, issued.AccessToken)
	require.NoError(t, err)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_reauth_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.action='identity.reauth' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER reject_reauth_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION reject_reauth_audit()`))
	_, err = f.service.Reauthenticate(f.ctx, claims, f.password, identity.RequestMeta{})
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []model.RefreshToken
	require.NoError(t, q.Select("id,reauth_at").Where("tenant_id=? AND user_id=? AND session_id=?", 1, f.user.ID, claims.SessionID).Find(&rows).Error)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		require.Nil(t, row.ReauthAt)
	}
}

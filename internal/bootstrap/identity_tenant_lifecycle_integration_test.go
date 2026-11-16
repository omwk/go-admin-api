package bootstrap

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantIntegrationSuspendAndResume(t *testing.T) {
	// S5 §4.8–9, §7.16: reject before token claim, preserve all data, then recover.
	f := newTenantFixture(t)
	template := f.template(t, "lifecycle")
	result := f.provision(t, template.ID, "lifecycle-b")
	tokens, password := f.accept(t, result, "lifecycle-b-admin")
	w, _ := f.request(t, "POST", tenantPath(result.TenantID)+"/disable", nil, "")
	require.Equal(t, 200, w.Code)
	before, found, err := f.identityFixture.store.RefreshByHash(f.ctx, refresh.Hash(tokens.RefreshToken))
	require.NoError(t, err)
	require.True(t, found)
	count := f.count(t, "sys_refresh_token", result.TenantID)
	for _, attempt := range []struct {
		path  string
		input any
	}{
		{"/auth/login", map[string]any{"tenant_id": result.TenantID, "account": "lifecycle-b-admin", "password": password}},
		{"/auth/refresh", map[string]any{"refresh_token": tokens.RefreshToken}},
	} {
		w, out := f.request(t, "POST", attempt.path, attempt.input, "")
		require.Equal(t, 403, w.Code)
		require.Equal(t, errs.ErrTenantSuspended, out.Code)
	}
	after, found, err := f.identityFixture.store.RefreshByHash(f.ctx, refresh.Hash(tokens.RefreshToken))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, before, after)
	require.Equal(t, count, f.count(t, "sys_refresh_token", result.TenantID))
	require.EqualValues(t, 1, f.count(t, "sys_user", result.TenantID))
	w, _ = f.request(t, "POST", tenantPath(result.TenantID)+"/enable", nil, "")
	require.Equal(t, 200, w.Code)
	w, _ = f.request(t, "POST", "/auth/login", map[string]any{"tenant_id": result.TenantID, "account": "lifecycle-b-admin", "password": password}, "")
	require.Equal(t, 200, w.Code)
	w, _ = f.request(t, "POST", "/auth/refresh", map[string]any{"refresh_token": tokens.RefreshToken}, "")
	require.Equal(t, 200, w.Code)
	t.Log("disable/login=403/20111 refresh=403/20111; refresh state unchanged; enable/login/refresh=200")
}

func TestIdentityTenantIntegrationInvitationExpiresAndIsBound(t *testing.T) {
	f := newTenantFixture(t)
	row := f.template(t, "bound-invite")
	result := f.provision(t, row.ID, "bound-b")
	raw := inviteRaw(t, result.InviteURL)
	password := accountPassword(t)
	for _, input := range []map[string]any{
		{"tenant_id": 1, "token": raw, "password": password},
		{"tenant_id": result.TenantID, "token": raw, "password": password, "username": "other-admin"},
	} {
		w, _ := f.request(t, "POST", "/auth/invite/accept", input, "")
		require.Equal(t, 400, w.Code)
	}
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE auth_token SET expires_at=? WHERE tenant_id=?", f.now.Add(-time.Second), result.TenantID))
	w, _ := f.request(t, "POST", "/auth/invite/accept", map[string]any{"tenant_id": result.TenantID, "token": raw, "password": password}, "")
	require.Equal(t, 400, w.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var user model.User
	require.NoError(t, q.Where("tenant_id=?", result.TenantID).Take(&user).Error)
	require.Equal(t, "disabled", user.Status)
	require.Empty(t, user.PasswordHash)
	var token model.AuthToken
	require.NoError(t, q.Where("tenant_id=?", result.TenantID).Take(&token).Error)
	require.Zero(t, token.UsedCount)
}

func TestIdentityTenantIntegrationCrossTenantAudit(t *testing.T) {
	f := newTenantFixture(t)
	row := f.template(t, "audit-template")
	result := f.provision(t, row.ID, "audited-b")
	for _, request := range []struct {
		method, path string
		input        any
	}{
		{"GET", tenantPath(result.TenantID), nil},
		{"GET", "/admin/tenants", nil},
		{"PUT", tenantPath(result.TenantID), map[string]any{"name": "reviewed"}},
		{"POST", tenantPath(result.TenantID) + "/disable", nil},
		{"POST", tenantPath(result.TenantID) + "/enable", nil},
	} {
		w, _ := f.request(t, request.method, request.path, request.input, "")
		require.Equal(t, 200, w.Code)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var events []struct {
		Action          string
		ResourceID      string
		ActorTenantName string
		Before, After   json.RawMessage
	}
	require.NoError(t, q.Table("audit_log").Select("action,resource_id,actor_tenant_name,\"before\",\"after\"").Where("resource_type='tenant' AND (resource_id=? OR action='system.tenant.list')", strconv.FormatInt(result.TenantID, 10)).Find(&events).Error)
	require.Len(t, events, 6)
	for _, event := range events {
		require.Equal(t, "default", event.ActorTenantName)
		if event.Action == "system.tenant.list" || event.Action == "system.tenant.read" {
			require.JSONEq(t, "null", string(event.Before))
			require.JSONEq(t, `{"accessed_tenants":[`+strconv.FormatInt(result.TenantID, 10)+`],"count":1}`, string(event.After))
		} else {
			require.NotEqual(t, string(event.Before), string(event.After))
		}
		for _, data := range []json.RawMessage{event.Before, event.After} {
			var fields map[string]any
			require.NoError(t, json.Unmarshal(data, &fields))
			require.NotContains(t, fields, "actor_tenant")
			require.NotContains(t, fields, "target_tenant")
			require.NotContains(t, string(data), inviteRaw(t, result.InviteURL))
			require.NotContains(t, string(data), "invite_url")
		}
	}
	t.Log("provision/read/list/rename/disable/enable: read scope metadata only; changed writes only; immutable actor tenant; no invitation credential")
}

package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantRecoveryIntegrationReissue(t *testing.T) {
	// BE-IDN-002 §3.1–2: an expired/lost invitation can be replaced and used once.
	f := newTenantFixture(t)
	template := f.template(t, "recovery")
	initial := f.provision(t, template.ID, "recovery-b")
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE auth_token SET expires_at=? WHERE tenant_id=?", f.now.Add(-time.Hour), initial.TenantID))
	reissued := recoveryInvite(t, f, initial.TenantID, "", "")
	require.Equal(t, f.now.AddDate(0, 0, 7), reissued.ExpiresAt)
	assertRecoveryLinkInvalid(t, f, initial.TenantID, initial.InviteURL)
	f.accept(t, identity.ProvisionTenantResult{TenantID: initial.TenantID, InviteURL: reissued.InviteURL}, "recovery-b-admin")
	user := recoveryAdmin(t, f, initial.TenantID)
	require.Equal(t, "active", user.Status)
	require.True(t, user.IsSuper)
	assertRecoveryLinkInvalid(t, f, initial.TenantID, reissued.InviteURL)
	t.Log("§3.1 reissue=201, accept=201, login=200, active super; §3.2 old/reused link=400 invalid")
}

func TestIdentityTenantRecoveryIntegrationForceAndSuspension(t *testing.T) {
	// BE-IDN-002 §3.3–4: force resets an existing administrator, suspension always wins.
	f := newTenantFixture(t)
	template := f.template(t, "force")
	initial := f.provision(t, template.ID, "force-b")
	f.accept(t, initial, "force-b-admin")
	w, _ := f.request(t, "POST", tenantPath(initial.TenantID)+"/admin-invite", nil, "")
	require.Equal(t, 409, w.Code)
	require.Contains(t, w.Body.String(), "该租户已有管理员")
	first := recoveryInvite(t, f, initial.TenantID, "?force=true", "")
	second := recoveryInvite(t, f, initial.TenantID, "?force=true", "")
	assertRecoveryLinkInvalid(t, f, initial.TenantID, first.InviteURL)
	f.accept(t, identity.ProvisionTenantResult{TenantID: initial.TenantID, InviteURL: second.InviteURL}, "force-b-admin")
	require.EqualValues(t, 1, f.count(t, "sys_user", initial.TenantID))
	for _, query := range []string{"?force=invalid", "?force="} {
		w, _ = f.request(t, "POST", tenantPath(initial.TenantID)+"/admin-invite"+query, nil, "")
		require.Equal(t, 400, w.Code)
	}
	w, _ = f.request(t, "POST", tenantPath(initial.TenantID)+"/disable", nil, "")
	require.Equal(t, 200, w.Code)
	before := f.count(t, "auth_token", initial.TenantID)
	for _, query := range []string{"", "?force=true"} {
		w, _ = f.request(t, "POST", tenantPath(initial.TenantID)+"/admin-invite"+query, nil, "")
		require.Equal(t, 409, w.Code)
		require.Contains(t, w.Body.String(), "租户已停用")
	}
	require.Equal(t, before, f.count(t, "auth_token", initial.TenantID))
	platform := recoveryInvite(t, f, 1, "?force=true", "")
	f.accept(t, identity.ProvisionTenantResult{TenantID: 1, InviteURL: platform.InviteURL}, identity.SeedAdminUsername)
	t.Log("§3.3 active admin=409, force=201, previous pending link invalid, existing account reset/login=200; platform recovery allowed; §3.4 suspended=409 with/without force")
}

func TestIdentityTenantRecoveryIntegrationAuditReplayAndIsolation(t *testing.T) {
	// BE-IDN-002 §3.5,9: exactly one audit per issuance, encrypted replay and tenant isolation.
	f := newTenantFixture(t)
	template := f.template(t, "recovery-audit")
	target := f.provision(t, template.ID, "audit-target")
	other := f.provision(t, template.ID, "audit-other")
	first := recoveryInvite(t, f, target.TenantID, "", "recovery-replay")
	replay := recoveryInvite(t, f, target.TenantID, "", "recovery-replay")
	require.Equal(t, first, replay)
	w, out := f.request(t, "POST", tenantPath(target.TenantID)+"/admin-invite?force=true", nil, "recovery-replay")
	require.Equal(t, 409, w.Code)
	require.Equal(t, 10006, out.Code)
	forced := recoveryInvite(t, f, target.TenantID, "?force=true", "")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var events []struct{ Before, After json.RawMessage }
	require.NoError(t, q.Table("audit_log").Select("\"before\",\"after\"").Where("action=? AND resource_id=?", "system:tenant:admin_invite", itoa(target.TenantID)).Order("id").Find(&events).Error)
	require.Len(t, events, 2)
	for i, event := range events {
		for _, data := range []json.RawMessage{event.Before, event.After} {
			var fields map[string]any
			require.NoError(t, json.Unmarshal(data, &fields))
			require.EqualValues(t, 1, fields["actor_tenant"])
			require.EqualValues(t, target.TenantID, fields["target_tenant"])
			require.NotContains(t, string(data), inviteRaw(t, first.InviteURL))
			require.NotContains(t, string(data), inviteRaw(t, forced.InviteURL))
		}
		var after map[string]any
		require.NoError(t, json.Unmarshal(event.After, &after))
		require.Equal(t, i == 1, after["force"])
	}
	var saved struct{ ResponseBody json.RawMessage }
	require.NoError(t, q.Table("idempotency_key").Select("response_body").Where("scope LIKE ?", "%POST:/api/v1"+tenantPath(target.TenantID)+"/admin-invite").Take(&saved).Error)
	require.NotContains(t, string(saved.ResponseBody), inviteRaw(t, first.InviteURL))
	require.NotContains(t, string(saved.ResponseBody), "invite_url")
	require.Contains(t, string(saved.ResponseBody), "cipher")
	var token model.AuthToken
	require.NoError(t, q.Select("token_hash,revoked_at").Where("tenant_id=? AND token_hash=?", other.TenantID, refresh.Hash(inviteRaw(t, other.InviteURL))).Take(&token).Error)
	require.Nil(t, token.RevokedAt)
	require.EqualValues(t, 1, f.count(t, "auth_token", other.TenantID))
	f.accept(t, other, "audit-other-admin")
	t.Log("§3.5 two issuances=two audits with actor_tenant/target_tenant/force; replay adds none and ciphertext contains no link; §3.9 other tenant original invitation/login unchanged")
}

func TestIdentityTenantRecoveryIntegrationAuditRollback(t *testing.T) {
	f := newTenantFixture(t)
	template := f.template(t, "recovery-rollback")
	initial := f.provision(t, template.ID, "recovery-rollback-b")
	failing, err := identity.NewAdminService(newAdminStore(f.database), func(context.Context, audit.Event) error { return errors.New("injected recovery audit failure") })
	require.NoError(t, err)
	sealer, err := crypto.NewKeyring(map[int][]byte{1: f.secret}, 1)
	require.NoError(t, err)
	service, err := identity.NewTenantService(newTenantStore(f.database), failing, 1, "", func() time.Time { return f.now }, sealer)
	require.NoError(t, err)
	f.router = gin.New()
	require.NoError(t, identity.RegisterTenantRoutes(f.router, f.identityFixture.service, f.profiles, service))
	before := tenantAllCounts(t, f)
	w, _ := f.request(t, "POST", tenantPath(initial.TenantID)+"/admin-invite", nil, "rollback-key")
	require.Equal(t, 500, w.Code)
	require.Equal(t, before, tenantAllCounts(t, f))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var token model.AuthToken
	require.NoError(t, q.Select("revoked_at").Where("tenant_id=?", initial.TenantID).Take(&token).Error)
	require.Nil(t, token.RevokedAt)
	t.Log("audit failure=500; revocation/new invite/idempotency all rolled back")
}

type recoveryInviteResult struct {
	InviteURL string    `json:"invite_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

func recoveryInvite(t *testing.T, f *tenantFixture, tenant int64, query, key string) recoveryInviteResult {
	t.Helper()
	w, out := f.request(t, "POST", tenantPath(tenant)+"/admin-invite"+query, nil, key)
	require.Equal(t, 201, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var result recoveryInviteResult
	require.NoError(t, json.Unmarshal(out.Data, &result))
	require.NotEmpty(t, result.InviteURL)
	return result
}

func assertRecoveryLinkInvalid(t *testing.T, f *tenantFixture, tenant int64, link string) {
	t.Helper()
	w, _ := f.request(t, "POST", "/auth/invite/accept", map[string]any{"tenant_id": tenant, "token": inviteRaw(t, link), "password": accountPassword(t)}, "")
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "邀请或链接无效")
}

func recoveryAdmin(t *testing.T, f *tenantFixture, tenant int64) model.User {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var user model.User
	require.NoError(t, q.Select("id,tenant_id,username,dept_id,is_super,status").Where("tenant_id=? AND is_super=true", tenant).Take(&user).Error)
	return user
}

package bootstrap

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestIdentityAccountInviteIntegrationPresetAndSingleUse(t *testing.T) {
	// F1 §5.1–2: preset role/department, immediate login, hash-only storage and single use.
	f := newAccountFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	role := model.Role{TenantID: 1, Code: "invite-member", Name: "member", DataScope: "self", Status: "active"}
	require.NoError(t, q.Create(&role).Error)
	w := f.request(t, "POST", "/admin/invites", f.token, map[string]any{"dept_id": f.user.DepartmentID, "role_ids": []int64{role.ID}, "username": "new-member"})
	require.Equal(t, 201, w.Code, w.Body.String())
	var invite identity.InviteView
	notificationDataF3(t, w, &invite)
	raw := inviteRaw(t, invite.Link)
	entropy, err := base64.RawURLEncoding.DecodeString(raw)
	require.NoError(t, err)
	require.Len(t, entropy, 32)
	stored, found, err := f.accounts.Store.TokenByID(f.ctx, 1, invite.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, refresh.Hash(raw), stored.TokenHash)
	password := accountPassword(t)
	accepted := f.request(t, "POST", "/auth/invite/accept", "", map[string]any{"token": raw, "password": password})
	require.Equal(t, 201, accepted.Code, accepted.Body.String())
	var user identity.UserSummary
	notificationDataF3(t, accepted, &user)
	persisted := accountUser(t, f, user.ID)
	require.Equal(t, f.user.DepartmentID, persisted.DepartmentID)
	require.False(t, persisted.ForcePasswordChange)
	var count int64
	require.NoError(t, q.Model(&model.UserRole{}).Where("tenant_id=1 AND user_id=? AND role_id=?", user.ID, role.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	login := f.request(t, "POST", "/auth/login", "", map[string]any{"account": user.Username, "password": password})
	require.Equal(t, 200, login.Code, login.Body.String())
	repeat := f.request(t, "POST", "/auth/invite/accept", "", map[string]any{"token": raw, "password": password})
	require.Equal(t, 400, repeat.Code)
	require.Contains(t, repeat.Body.String(), "邀请或链接无效")
}

func TestIdentityAccountInviteIntegrationExpiryRevokeAndBatch(t *testing.T) {
	// F1 §5.2–3: no post-expiry/revocation use; bounded and unlimited batch invites.
	f := newAccountFixture(t)
	expired := f.invite(t, 1)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE auth_token SET expires_at=? WHERE id=?", f.now.Add(-time.Second), expired.ID))
	revoked := f.invite(t, 1)
	w := f.request(t, "DELETE", fmt.Sprintf("/admin/invites/%d", revoked.ID), f.token, nil)
	require.Equal(t, 204, w.Code, w.Body.String())
	for _, invite := range []identity.InviteView{expired, revoked} {
		_, err := f.accounts.AcceptInvite(f.ctx, 1, identity.AcceptInviteInput{Token: inviteRaw(t, invite.Link), Username: "blocked", Password: accountPassword(t)}, identity.RequestMeta{IP: "192.0.2.15"})
		require.ErrorContains(t, err, "邀请或链接无效")
	}
	batch := f.invite(t, 3)
	for i := 0; i < 4; i++ {
		out := f.request(t, "POST", "/auth/invite/accept", "", map[string]any{"token": inviteRaw(t, batch.Link), "username": fmt.Sprintf("batch-%d", i), "password": accountPassword(t)})
		if i < 3 {
			require.Equal(t, 201, out.Code, out.Body.String())
		} else {
			require.Equal(t, 400, out.Code)
		}
	}
	unlimited := f.invite(t, 0)
	for i := 0; i < 2; i++ {
		_, err := f.accounts.AcceptInvite(f.ctx, 1, identity.AcceptInviteInput{Token: inviteRaw(t, unlimited.Link), Username: fmt.Sprintf("unlimited-%d", i), Password: accountPassword(t)}, identity.RequestMeta{IP: "192.0.2.19"})
		require.NoError(t, err)
	}
	list := f.request(t, "GET", "/admin/invites", f.token, nil)
	require.Equal(t, 200, list.Code)
	require.Contains(t, list.Body.String(), `"status":"revoked"`)
	require.NotContains(t, list.Body.String(), "token_hash")
}

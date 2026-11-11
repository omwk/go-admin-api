package bootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

type accountFixture struct {
	*identityFixture
	accounts      *identity.AccountService
	actor         identity.Overview
	router        *gin.Engine
	token         string
	notifications *notificationManager
}

func newAccountFixture(t *testing.T) *accountFixture {
	t.Helper()
	f := newIdentityFixture(t)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET is_super=true WHERE id=?", f.user.ID))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	role := model.Role{TenantID: 1, Code: "account-owner", Name: "owner", DataScope: "all", Status: "active"}
	require.NoError(t, q.Create(&role).Error)
	require.NoError(t, q.Create(&model.UserRole{TenantID: 1, UserID: f.user.ID, RoleID: role.ID}).Error)
	admin, err := identity.NewAdminService(newAdminStore(f.database), func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, f.database, event) })
	require.NoError(t, err)
	sealer, err := crypto.NewKeyring(map[int][]byte{1: f.secret}, 1)
	require.NoError(t, err)
	notifications := newNotificationManager(&notificationStore{database: f.database}, sealer, func() time.Time { return f.now })
	a := &identity.AccountService{Store: &accountStore{f.store}, Auth: f.service, Admin: admin, Limits: f.redis, Now: func() time.Time { return f.now }, CopyResetLinks: true,
		PublicURL: "https://accounts.example.invalid", Notify: &accountNotifications{manager: notifications}}
	f.service.UseAccounts(a)
	admin.UseAccounts(a)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	actor, err := profiles.Me(f.ctx, 1, f.user.ID)
	require.NoError(t, err)
	router := gin.New()
	identity.RegisterAuthRoutes(router, f.service, 1)
	identity.RegisterAccountSelfRoutes(router, f.service, a)
	identity.RegisterRecoveryRoutes(router, a, 1)
	require.NoError(t, identity.RegisterInviteRoutes(router, f.service, profiles, a, 1))
	result := &accountFixture{identityFixture: f, accounts: a, actor: actor, router: router, notifications: notifications}
	result.token = f.login(t).AccessToken
	return result
}

func (f *accountFixture) request(t *testing.T, method, path, token string, value any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	request := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(data)).WithContext(f.ctx)
	request.RemoteAddr = "192.0.2.8:1234"
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	out := httptest.NewRecorder()
	f.router.ServeHTTP(out, request)
	return out
}

func (f *accountFixture) invite(t *testing.T, uses int) identity.InviteView {
	t.Helper()
	w := f.request(t, "POST", "/admin/invites", f.token, map[string]any{"dept_id": f.user.DepartmentID, "role_ids": []int64{}, "max_uses": uses})
	require.Equal(t, 201, w.Code, w.Body.String())
	var out identity.InviteView
	notificationDataF3(t, w, &out)
	return out
}

func accountPassword(t *testing.T) string {
	t.Helper()
	value := make([]byte, 10)
	_, err := rand.Read(value)
	require.NoError(t, err)
	return "A1" + hex.EncodeToString(value)
}

func inviteRaw(t *testing.T, link string) string {
	t.Helper()
	_, raw, ok := strings.Cut(link, "#token=")
	require.True(t, ok)
	return raw
}

func accountUser(t *testing.T, f *accountFixture, id int64) model.User {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var out model.User
	require.NoError(t, q.Select("id,tenant_id,username,email,phone,dept_id,force_password_change,password_changed_at").Where("tenant_id=1 AND id=?", id).Take(&out).Error)
	return out
}

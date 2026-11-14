package bootstrap

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestIdentityRegistrationIntegrationDefaultAndLifecycle(t *testing.T) {
	f := newRegistrationFixture(t)
	in := registrationInput(t, "registered")
	w, out := f.call(t, "POST", "/auth/register", in)
	require.Equal(t, 403, w.Code)
	require.Equal(t, 20111, out.Code)
	require.Equal(t, "该企业未开放自助注册", out.Message)
	f.settings(t, map[string]any{"allow_self_registration": true})
	result := f.register(t, in, 201)
	user := registrationUser(t, f, 1, in.Account)
	require.Equal(t, "disabled", user.Status)
	require.False(t, user.IsSuper)
	require.NoError(t, pwd.Verify(user.PasswordHash, in.Password))
	require.EqualValues(t, 1, registrationCount(t, f, "password_history", 1, user.ID))
	raw := inviteRaw(t, result.Link)
	row, found, err := f.accounts.Store.TokenByHash(f.ctx, 1, "verify", refresh.Hash(raw))
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, refresh.Hash(raw) == row.TokenHash)
	require.Equal(t, 24*time.Hour, row.ExpiresAt.Sub(row.CreatedAt))
	require.Equal(t, 1, row.MaxUses)
	login := map[string]any{"tenant": in.Tenant, "account": in.Account, "password": in.Password}
	w, out = f.call(t, "POST", "/auth/login", login)
	require.Equal(t, 403, w.Code)
	require.Equal(t, 20111, out.Code)
	require.Equal(t, "请先完成邮箱验证", out.Message)
	login["password"] = accountPassword(t)
	w, out = f.call(t, "POST", "/auth/login", login)
	require.Equal(t, 401, w.Code)
	require.Equal(t, "企业代码、账号或密码错误", out.Message)
	f.verify(t, result.Link, 204)
	user = registrationUser(t, f, 1, in.Account)
	require.Equal(t, "active", user.Status)
	require.False(t, user.IsSuper)
	require.Zero(t, registrationCount(t, f, "sys_user_role", 1, user.ID))
	login["password"] = in.Password
	w, out = f.call(t, "POST", "/auth/login", login)
	require.Equal(t, 200, w.Code, out.Code, out.Message)
	f.verify(t, result.Link, 400)
	t.Log("§4 #1-4/#6: default closed; 201 disabled; unverified correct password 403; verify 204; login 200; no default role; token single use")
}

func TestIdentityRegistrationIntegrationDefaultRoleAndIsolation(t *testing.T) {
	f := newRegistrationFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	role := model.Role{TenantID: 1, Code: "registration-reader", Name: "reader", Status: "active", DataScope: "self"}
	require.NoError(t, q.Create(&role).Error)
	f.settings(t, map[string]any{"allow_self_registration": true, "default_role_code": role.Code})
	in := registrationInput(t, "shared-name")
	first := f.register(t, in, 201)
	secondTenant := registrationTenant(t, f, "registration-other")
	other := in
	other.Tenant = "registration-other"
	second := f.register(t, other, 201)
	f.verify(t, first.Link, 204)
	f.verify(t, second.Link, 204)
	one := registrationUser(t, f, 1, in.Account)
	two := registrationUser(t, f, secondTenant, in.Account)
	require.False(t, one.IsSuper)
	require.True(t, two.IsSuper)
	require.NotEqual(t, one.ID, two.ID)
	var links []model.UserRole
	require.NoError(t, q.Where("tenant_id=? AND user_id=?", 1, one.ID).Find(&links).Error)
	require.Len(t, links, 1)
	require.Equal(t, role.ID, links[0].RoleID)
	for _, code := range []string{in.Tenant, other.Tenant} {
		w, out := f.call(t, "POST", "/auth/login", map[string]any{"tenant": code, "account": in.Account, "password": in.Password})
		require.Equal(t, 200, w.Code, out.Code, out.Message)
	}
	t.Log("§4 #5-7: tenant-local default role; same account/email registered in distinct tenants; empty second tenant elects a super only at verification")
}

func TestIdentityRegistrationIntegrationDuplicateAccountAndEmail(t *testing.T) {
	f := newRegistrationFixture(t)
	f.settings(t, map[string]any{"allow_self_registration": true})
	in := registrationInput(t, "repeat-register")
	first := f.register(t, in, 201)
	in.Password = accountPassword(t)
	second := f.register(t, in, 202)
	user := registrationUser(t, f, 1, in.Account)
	require.Error(t, pwd.Verify(user.PasswordHash, in.Password))
	duplicateEmail := in
	duplicateEmail.Account = "same-email-other-name"
	third := f.register(t, duplicateEmail, 202)
	require.EqualValues(t, 2, registrationCount(t, f, "sys_user", 1, 0))
	require.EqualValues(t, 3, registrationCount(t, f, "auth_token", 1, user.ID))
	for _, link := range []string{first.Link, second.Link} {
		f.verify(t, link, 400)
	}
	f.verify(t, third.Link, 204)
	activeDuplicate := f.register(t, in, 202)
	f.verify(t, activeDuplicate.Link, 400)
	require.EqualValues(t, 3, registrationCount(t, f, "auth_token", 1, user.ID))
	var shapeOne, shapeTwo map[string]any
	raw, err := json.Marshal(first)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &shapeOne))
	raw, err = json.Marshal(second)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &shapeTwo))
	require.Len(t, shapeOne, 2)
	require.Len(t, shapeTwo, 2)
	require.Contains(t, shapeOne, "accepted")
	require.Contains(t, shapeTwo, "accepted")
	require.Contains(t, shapeOne, "dev_verify_url")
	require.Contains(t, shapeTwo, "dev_verify_url")
	t.Log("§4 #8/#11 (confirmed A): duplicates 202; same data keys; pending email resends; one user; password unchanged; older links revoked; active duplicate gets a dummy dev link")
}

func TestIdentityRegistrationIntegrationTenantLimitAndUnknownTenant(t *testing.T) {
	f := newRegistrationFixture(t)
	f.settings(t, map[string]any{"allow_self_registration": true, "registration_hourly_limit": 2})
	in := registrationInput(t, "limited")
	f.register(t, in, 201)
	f.register(t, in, 202)
	w, out := f.call(t, "POST", "/auth/register", registrationInput(t, "over-limit"))
	require.Equal(t, 429, w.Code)
	require.Equal(t, 10005, out.Code)
	other := in
	other.Tenant = "budget-other"
	registrationTenant(t, f, other.Tenant)
	f.register(t, other, 201)
	f.server.FastForward(time.Hour)
	f.register(t, registrationInput(t, "after-hour"), 201)
	unknown := in
	unknown.Tenant = "not-provisioned"
	w, out = f.call(t, "POST", "/auth/register", unknown)
	require.Equal(t, 401, w.Code)
	require.Equal(t, 20101, out.Code)
	login, loginOut := f.call(t, "POST", "/auth/login", map[string]any{"tenant": unknown.Tenant, "account": in.Account, "password": in.Password})
	require.Equal(t, login.Code, w.Code)
	require.Equal(t, loginOut.Code, out.Code)
	require.Equal(t, loginOut.Message, out.Message)
	t.Log("§4 #9-10: duplicate attempts count toward exact tenant hourly quota; 429/10005; independent tenant budget and expiry; unknown tenant matches login 401/20101/message")
}

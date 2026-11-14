package bootstrap

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

func profileFieldsFixture(t *testing.T) *accountFixture {
	t.Helper()
	f := newAccountFixture(t)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	identity.RegisterProfileRoutes(f.router, f.service, profiles)
	return f
}

func profileFields(t *testing.T, f *accountFixture, bearer string) map[string]any {
	t.Helper()
	w := f.request(t, "GET", "/auth/me", bearer, nil)
	require.Equal(t, 200, w.Code)
	var data map[string]any
	notificationDataF3(t, w, &data)
	user, ok := data["user"].(map[string]any)
	require.True(t, ok)
	for _, key := range []string{"email", "phone", "dept_name"} {
		require.Contains(t, user, key)
	}
	return user
}

func profilePhone(t *testing.T) string {
	t.Helper()
	local := "1" + strings.Map(func(r rune) rune { return '0' + r%10 }, accountPassword(t))[:10]
	normalized, err := identity.NormalizePhone(local)
	require.NoError(t, err)
	return normalized
}

func TestProfileFieldsIntegrationDatabaseAndNulls(t *testing.T) {
	// BE-IDN-003 §4.1-2: contacts are explicit nulls until set, then match the current owner.
	f := profileFieldsFixture(t)
	user := profileFields(t, f, f.token)
	require.Nil(t, user["email"])
	require.Nil(t, user["phone"])
	require.Equal(t, "root", user["dept_name"])
	email, phone := "member@example.invalid", profilePhone(t)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET email=?,phone=? WHERE id=?", email, phone, f.user.ID))
	user = profileFields(t, f, f.token)
	require.Equal(t, email, user["email"])
	require.True(t, user["phone"] == phone)
	require.Equal(t, float64(f.user.ID), user["id"])
	for _, key := range []string{"token", "access_token", "refresh_token", "password_hash", "totp_secret"} {
		require.NotContains(t, user, key)
	}
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET email='',phone='' WHERE id=?", f.user.ID))
	user = profileFields(t, f, f.token)
	require.Nil(t, user["email"])
	require.Nil(t, user["phone"])
}

func TestProfileFieldsIntegrationOneJoinQuery(t *testing.T) {
	// BE-IDN-003 §4.3: all roles and department metadata use one bounded identity query.
	f := profileFieldsFixture(t)
	capture := &sqlCapture{Interface: gormlog.Discard}
	store := &profileStore{query: func(ctx context.Context) (*gorm.DB, context.CancelFunc) {
		q, cancel := f.database.WithContext(ctx)
		return q.Session(&gorm.Session{Logger: capture}), cancel
	}}
	profiles, err := identity.NewProfileService(store)
	require.NoError(t, err)
	_, err = profiles.Me(f.ctx, 1, f.user.ID)
	require.NoError(t, err)
	require.Len(t, capture.statements, 1)
	require.Contains(t, capture.statements[0], "LEFT JOIN sys_dept")
	require.Contains(t, capture.statements[0], "d.tenant_id = u.tenant_id")
	foreign, err := store.Identity(f.ctx, 2, f.user.ID)
	require.NoError(t, err)
	require.Empty(t, foreign)
	t.Log("profile SQL count = 1 (budget <= 2), department joined with tenant predicate")
}

func TestProfilePhoneIntegrationUpdateReloginClearAndAudit(t *testing.T) {
	// BE-IDN-003 §4.4/7: update and clear persist, retain login aliases and emit safe atomic audit.
	f := profileFieldsFixture(t)
	phone := profilePhone(t)
	response := f.request(t, "PUT", "/auth/profile", f.token, map[string]any{"nickname": "member", "phone": phone})
	require.Equal(t, 200, response.Code)
	stored := accountUser(t, f, f.user.ID)
	require.NotNil(t, stored.Phone)
	require.True(t, *stored.Phone == phone)
	tokens, err := f.service.Login(f.ctx, 1, phone, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	user := profileFields(t, f, tokens.AccessToken)
	require.True(t, user["phone"] == phone)
	require.Equal(t, f.user.Username, user["username"])
	for _, input := range []map[string]any{{"nickname": "retained"}, {"phone": nil}} {
		response = f.request(t, "PUT", "/auth/profile", tokens.AccessToken, input)
		require.Equal(t, 200, response.Code)
		require.True(t, profileFields(t, f, tokens.AccessToken)["phone"] == phone)
	}
	response = f.request(t, "PUT", "/auth/profile", tokens.AccessToken, map[string]any{"phone": ""})
	require.Equal(t, 200, response.Code)
	require.Nil(t, accountUser(t, f, f.user.ID).Phone)
	require.Nil(t, profileFields(t, f, tokens.AccessToken)["phone"])
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []struct {
		Before, After json.RawMessage
	}
	require.NoError(t, q.Table("audit_log").Select("\"before\",\"after\"").Where("tenant_id=1 AND actor_id=? AND action=? AND resource_id=?", f.user.ID, "system:user:profile", strconv.FormatInt(f.user.ID, 10)).Order("id").Find(&rows).Error)
	require.Len(t, rows, 4)
	var cleared map[string]any
	require.NoError(t, json.Unmarshal(rows[len(rows)-1].After, &cleared))
	require.Contains(t, cleared, "phone")
	require.Nil(t, cleared["phone"])
	for _, row := range rows {
		for _, forbidden := range []string{"password", "token", "secret"} {
			require.NotContains(t, strings.ToLower(string(row.Before)+string(row.After)), forbidden)
		}
	}
}

func TestProfilePhoneIntegrationInvalidAndDuplicate(t *testing.T) {
	// BE-IDN-003 §4.5-6: malformed input and the tenant-local phone unique index have stable errors.
	f := profileFieldsFixture(t)
	response := f.request(t, "PUT", "/auth/profile", f.token, map[string]any{"phone": "12345"})
	require.Equal(t, 400, response.Code)
	var failure map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
	require.Equal(t, float64(10001), failure["code"])
	phone := profilePhone(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	other := model.User{TenantID: 1, Username: "other", PasswordHash: f.user.PasswordHash, DepartmentID: f.user.DepartmentID, Status: "active", Phone: &phone}
	require.NoError(t, q.Create(&other).Error)
	response = f.request(t, "PUT", "/auth/profile", f.token, map[string]any{"phone": phone})
	require.Equal(t, 409, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
	require.Equal(t, float64(10004), failure["code"])
	require.Equal(t, "该手机号已被使用", failure["message"])
	require.Nil(t, accountUser(t, f, f.user.ID).Phone)
	var count int64
	require.NoError(t, q.Table("audit_log").Where("action=?", "system:user:profile").Count(&count).Error)
	require.Zero(t, count, "failed updates must not emit successful profile audit")
}

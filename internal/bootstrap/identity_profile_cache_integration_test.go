package bootstrap

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
	"github.com/stretchr/testify/require"
)

func TestProfileFieldsIntegrationCachedProjectionUpgrade(t *testing.T) {
	// BE-IDN-003: deployments must also fill contacts when a pre-change cached user has tenant_code.
	f := newIdentityFixture(t)
	email, phone := "cached@example.invalid", profilePhone(t)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET email=?,phone=? WHERE id=?", email, phone, f.user.ID))
	store := newProfileStore(f.database)
	profiles, err := identity.NewProfileService(store)
	require.NoError(t, err)
	versions := authz.New(f.redis.cache)
	profiles.UseAuthz(versions, store.account)
	_, err = versions.Load(f.ctx, 1, f.user.ID, "perm", func(context.Context) ([]byte, error) {
		return json.Marshal(map[string]any{
			"user":        map[string]any{"id": f.user.ID, "tenant_id": 1, "tenant_code": "default", "username": f.user.Username, "dept_id": f.user.DepartmentID, "is_super": false, "roles": []any{}},
			"permissions": []string{},
		})
	})
	require.NoError(t, err)
	profile, err := profiles.Me(f.ctx, 1, f.user.ID)
	require.NoError(t, err)
	require.NotNil(t, profile.User.Email)
	require.NotNil(t, profile.User.Phone)
	require.NotNil(t, profile.User.DepartmentName)
	require.Equal(t, email, *profile.User.Email)
	require.True(t, phone == *profile.User.Phone)
	require.Equal(t, "root", *profile.User.DepartmentName)
	t.Log("pre-change profile cache with tenant_code is re-read for the new contact and department fields")
}

func TestProfilePhoneIntegrationInvalidatesCache(t *testing.T) {
	f := newAccountFixture(t)
	store := newProfileStore(f.database)
	profiles, err := identity.NewProfileService(store)
	require.NoError(t, err)
	versions := authz.New(f.redis.cache)
	profiles.UseAuthz(versions, store.account)
	f.accounts.Invalidate = versions.Pending
	identity.RegisterProfileRoutes(f.router, f.service, profiles)
	require.Nil(t, profileFields(t, f, f.token)["phone"])
	phone := profilePhone(t)
	response := f.request(t, "PUT", "/auth/profile", f.token, map[string]any{"phone": phone})
	require.Equal(t, 200, response.Code)
	require.True(t, profileFields(t, f, f.token)["phone"] == phone)
	response = f.request(t, "PUT", "/auth/profile", f.token, map[string]any{"phone": ""})
	require.Equal(t, 200, response.Code)
	require.Nil(t, profileFields(t, f, f.token)["phone"])
	t.Log("a warm /me cache reflects committed phone updates and clearing immediately")
}

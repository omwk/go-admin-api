package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

type failedInvalidations struct{ authz.Backend }

func (f failedInvalidations) IncrBatch(_ context.Context, keys []string) []cache.IncrementResult {
	results := make([]cache.IncrementResult, len(keys))
	for i, key := range keys {
		results[i] = cache.IncrementResult{Key: key, Err: errors.New("injected post-commit failure")}
	}
	return results
}

func invalidationCounter(t *testing.T) float64 {
	t.Helper()
	metrics, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, metric := range metrics {
		if metric.GetName() == "authz_invalidate_failed_total" {
			return metric.Metric[0].GetCounter().GetValue()
		}
	}
	t.Fatal("authorization failure metric is absent")
	return 0
}

func cachedAdminRoutes(t *testing.T, f *adminFixture) *identity.ProfileService {
	t.Helper()
	store := newProfileStore(f.database)
	profiles, err := identity.NewProfileService(store)
	require.NoError(t, err)
	profiles.UseAuthz(authz.New(f.redis.cache), store.account)
	router := identityRouter(t, f.identityFixture)
	identity.RegisterProfileRoutes(router, f.identityFixture.service, profiles)
	require.NoError(t, identity.RegisterAdminRoutes(router, f.identityFixture.service, profiles, f.service))
	f.router = router
	return profiles
}

func TestAuthzIntegrationFailureReturnsCommittedSuccessAndConverges(t *testing.T) {
	// REQ-IAM-009: a successful DB grant never becomes a replay-inducing 503.
	f := newAdminFixture(t)
	profiles := cachedAdminRoutes(t, f)
	role := installAuthzUsers(t, f, 1)[0]
	before, err := profiles.Me(f.ctx, 1, role.ID)
	require.NoError(t, err)
	require.Contains(t, before.Permissions, permission.SystemUserList)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var assigned model.UserRole
	require.NoError(t, q.Select("tenant_id,user_id,role_id").Where("tenant_id = ? AND user_id = ?", 1, role.ID).Take(&assigned).Error)
	counter := invalidationCounter(t)
	f.service.UseInvalidator(authz.New(failedInvalidations{Backend: f.redis.cache}).Pending)
	response, _ := adminRequest(t, f, "PUT", resourcePath("roles", assigned.RoleID)+"/permissions", map[string]any{"permission_ids": []int64{}}, "failed-invalidation")
	require.Equal(t, 204, response.Code)
	require.Equal(t, "pending", response.Header().Get("X-Authz-Invalidation"))
	require.Equal(t, counter+1, invalidationCounter(t))
	var count int64
	require.NoError(t, q.Model(&model.RolePermission{}).Where("tenant_id = ? AND role_id = ?", 1, assigned.RoleID).Count(&count).Error)
	require.Zero(t, count)
	stale, err := profiles.Me(f.ctx, 1, role.ID)
	require.NoError(t, err)
	require.Contains(t, stale.Permissions, permission.SystemUserList)
	f.server.FastForward(5*time.Minute + time.Millisecond)
	current, err := profiles.Me(f.ctx, 1, role.ID)
	require.NoError(t, err)
	require.NotContains(t, current.Permissions, permission.SystemUserList)
}

func TestAuthzIntegrationAllMutationSourcesSharePermissionAndMenuVersion(t *testing.T) {
	f := newAdminFixture(t)
	profiles := cachedAdminRoutes(t, f)
	user := installAuthzUsers(t, f, 1)[0]
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var link model.UserRole
	require.NoError(t, q.Select("tenant_id,user_id,role_id").Where("tenant_id = ? AND user_id = ?", 1, user.ID).Take(&link).Error)
	menu := createAdminMenu(t, f, "authz-menu", 0)
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"permission", "PUT", resourcePath("roles", link.RoleID) + "/permissions", map[string]any{"permission_ids": []int64{}}},
		{"menu grant", "PUT", resourcePath("roles", link.RoleID) + "/menus", map[string]any{"menu_ids": []int64{menu.ID}}},
		{"custom departments", "PUT", resourcePath("roles", link.RoleID) + "/depts", map[string]any{"dept_ids": []int64{user.DepartmentID}}},
		{"role scope", "PUT", resourcePath("roles", link.RoleID), identity.RoleRequest{Code: "authz-load", Name: "scope", DataScope: "custom", Status: "active"}},
		{"role status", "PUT", resourcePath("roles", link.RoleID), identity.RoleRequest{Code: "authz-load", Name: "status", DataScope: "custom", Status: "disabled"}},
		{"menu configuration", "PUT", resourcePath("menus", menu.ID), identity.MenuRequest{Name: "changed", Type: "menu", Status: "active", Visible: true}},
		{"user roles", "PUT", resourcePath("users", user.ID) + "/roles", map[string]any{"role_ids": []int64{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := profiles.Menus(f.ctx, 1, user.ID)
			require.NoError(t, err)
			old, _, err := f.redis.cache.Version(f.ctx, authz.VersionKey(1, user.ID))
			require.NoError(t, err)
			response, _ := adminRequest(t, f, tc.method, tc.path, tc.body, "")
			require.Less(t, response.Code, 300, response.Body.String())
			newVersion, _, err := f.redis.cache.Version(f.ctx, authz.VersionKey(1, user.ID))
			require.NoError(t, err)
			require.NotEqual(t, old, newVersion)
			current, err := profiles.Menus(f.ctx, 1, user.ID)
			require.NoError(t, err)
			if tc.name == "permission" {
				require.NotContains(t, current.Permissions, permission.SystemUserList)
			}
		})
	}
}

func TestAuthzIntegrationCachedReadsFailClosedAndCheckCurrentAccount(t *testing.T) {
	f := newAdminFixture(t)
	profiles := cachedAdminRoutes(t, f)
	_, err := profiles.Me(f.ctx, 1, f.actor.User.ID)
	require.NoError(t, err)
	f.server.SetError("injected Redis dependency failure")
	_, err = profiles.Me(f.ctx, 1, f.actor.User.ID)
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	f.server.SetError("")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Model(&model.User{}).Where("tenant_id = ? AND id = ?", 1, f.actor.User.ID).Update("status", "disabled").Error)
	_, err = profiles.Me(f.ctx, 1, f.actor.User.ID)
	require.Equal(t, 20103, errs.From(err).Code)
}

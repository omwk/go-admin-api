package bootstrap

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func installAuthzUsers(t *testing.T, f *adminFixture, count int) []model.User {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	role := model.Role{TenantID: 1, Code: "authz-load", Name: "load", DataScope: "all", Status: "active"}
	require.NoError(t, q.Create(&role).Error)
	var point model.Permission
	require.NoError(t, q.Select("id").Where("tenant_id = ? AND code = ?", 1, permission.SystemUserList).Take(&point).Error)
	require.NoError(t, q.Create(&model.RolePermission{TenantID: 1, RoleID: role.ID, PermissionID: point.ID}).Error)
	users, links := make([]model.User, count), make([]model.UserRole, count)
	base := time.Now().UnixMicro() * 1000
	// Keep real-Redis benchmark keys disjoint while preserving GENERATED ALWAYS identity semantics.
	require.NoError(t, q.Exec("SELECT setval(pg_get_serial_sequence('sys_user','id'), ?, false)", base).Error)
	for i := range users {
		users[i] = model.User{TenantID: 1, Username: fmt.Sprintf("authz-load-%d", i), DepartmentID: f.user.DepartmentID, PasswordHash: f.user.PasswordHash, Status: "active"}
	}
	require.NoError(t, q.CreateInBatches(&users, 500).Error)
	for i := range users {
		links[i] = model.UserRole{TenantID: 1, RoleID: role.ID, UserID: users[i].ID}
	}
	require.NoError(t, q.CreateInBatches(&links, 500).Error)
	return users
}

type coldReadResult struct {
	elapsed time.Duration
	err     error
}

func parallelColdReads(ctx context.Context, profiles *identity.ProfileService, users []model.User, committed time.Time) []coldReadResult {
	jobs := make(chan model.User, len(users))
	for _, user := range users {
		jobs <- user
	}
	close(jobs)
	results := make(chan coldReadResult, len(users))
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for user := range jobs {
				value, err := profiles.Menus(ctx, 1, user.ID)
				for _, code := range value.Permissions {
					if code == permission.SystemUserList {
						err = fmt.Errorf("stale authorization after commit")
					}
				}
				results <- coldReadResult{elapsed: time.Since(committed), err: err}
			}
		}()
	}
	workers.Wait()
	close(results)
	out := []coldReadResult{}
	for result := range results {
		out = append(out, result)
	}
	return out
}

func runAuthzLatency(t *testing.T, f *adminFixture, backend *cache.Cache, count int) []time.Duration {
	t.Helper()
	users := installAuthzUsers(t, f, count)
	store := newProfileStore(f.database)
	profiles, err := identity.NewProfileService(store)
	require.NoError(t, err)
	versions := authz.New(backend)
	profiles.UseAuthz(versions, store.account)
	f.service.UseInvalidator(versions.Pending)
	oldVersions := make(map[int64]string, len(users))
	for _, user := range users {
		value, err := profiles.Menus(f.ctx, 1, user.ID)
		require.NoError(t, err)
		require.Contains(t, value.Permissions, permission.SystemUserList)
		version, _, err := backend.Version(f.ctx, authz.VersionKey(1, user.ID))
		require.NoError(t, err)
		oldVersions[user.ID] = version
	}
	t.Cleanup(func() { cleanupVersions(t, f, backend, oldVersions) })
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var link model.UserRole
	require.NoError(t, q.Select("tenant_id,user_id,role_id").Where("tenant_id = ? AND user_id = ?", 1, users[0].ID).Take(&link).Error)
	var committed time.Time
	transaction := f.store.transaction
	f.store.transaction = func(ctx context.Context, run func(context.Context) error) error {
		err := transaction(ctx, run)
		if err == nil {
			committed = time.Now()
		}
		return err
	}
	response, _ := adminRequest(t, f, "PUT", resourcePath("roles", link.RoleID)+"/permissions", map[string]any{"permission_ids": []int64{}}, "")
	require.Equal(t, 204, response.Code)
	require.Empty(t, response.Header().Get("X-Authz-Invalidation"))
	require.False(t, committed.IsZero())
	results := parallelColdReads(f.ctx, profiles, users, committed)
	times := make([]time.Duration, 0, len(results))
	for _, result := range results {
		require.NoError(t, result.err)
		times = append(times, result.elapsed)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	t.Logf("commit→cold-read users=%d readers=16 P50=%s P95=%s max=%s", count, times[(count-1)*50/100], times[(count-1)*95/100], times[count-1])
	require.Less(t, times[count-1], 5*time.Second)
	return times
}

func cleanupVersions(t *testing.T, f *adminFixture, backend *cache.Cache, versions map[int64]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(f.ctx), 30*time.Second)
	defer cancel()
	for user, old := range versions {
		key := authz.VersionKey(1, user)
		current, _, err := backend.Version(ctx, key)
		require.NoError(t, err)
		for _, version := range []string{old, current} {
			for _, kind := range []string{"perm", "menu"} {
				_, err := backend.Del(ctx, fmt.Sprintf("authz:%s:1:%d:v%s", kind, user, version))
				require.NoError(t, err)
			}
		}
		_, err = backend.Del(ctx, key)
		require.NoError(t, err)
	}
}

func TestAuthzIntegration200UserLatencyMatrix(t *testing.T) {
	f := newAdminFixture(t)
	runAuthzLatency(t, f, f.redis.cache, 200)
}

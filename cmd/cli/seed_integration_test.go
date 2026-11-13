package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestSeedCLIIntegrationUpgradeInvalidatesCachedMenus(t *testing.T) {
	// FOLLOWUP-SEED §3.1/2/6: cached menus and permissions change in the existing session.
	f := newSeedFixture(t)
	f.legacyCatalog(t)
	api := f.api(t)
	bearer := cliLogin(t, api.Client(), api.URL, f.password)
	before := seedProfile(t, api, "menus", bearer)
	seedProfile(t, api, "me", bearer)
	for _, path := range seedUpgradePaths() {
		require.NotContains(t, seedMenuPaths(before.Menus), path)
	}
	for _, code := range seedUpgradeCodes() {
		require.NotContains(t, before.Permissions, code)
	}
	key := authz.VersionKey(1, f.admin.ID)
	version, err := strconv.ParseInt(seedVersion(t, f.server, key), 10, 64)
	require.NoError(t, err)
	oldPayloads := []string{}
	for _, cached := range f.server.Keys() {
		if strings.HasPrefix(cached, "authz:menu:") || strings.HasPrefix(cached, "authz:perm:") {
			oldPayloads = append(oldPayloads, cached)
		}
	}
	require.Len(t, oldPayloads, 2)
	replacement, err := refresh.Generate()
	require.NoError(t, err)
	t.Setenv("CH_SEED_ADMIN_PASSWORD", replacement)
	f.seed(t)
	require.Equal(t, strconv.FormatInt(version+1, 10), seedVersion(t, f.server, key))
	current := seedProfile(t, api, "menus", bearer)
	require.Subset(t, seedMenuPaths(current.Menus), seedUpgradePaths())
	require.Len(t, current.Permissions, len(permission.All()))
	for _, code := range seedUpgradeCodes() {
		require.Contains(t, current.Permissions, code)
	}
	require.Equal(t, current.Permissions, seedProfile(t, api, "me", bearer).Permissions)
	for _, cached := range oldPayloads {
		require.True(t, f.server.Exists(cached), "INCR must retain the previous payload")
		require.Positive(t, f.server.TTL(cached), "previous payloads expire by TTL")
	}
	require.Zero(t, f.server.TTL(key), "version keys must not expire")
	counts := cliSeedRowCounts(f.ctx, t, f.database)
	f.seed(t)
	require.Equal(t, counts, cliSeedRowCounts(f.ctx, t, f.database))
	require.Equal(t, strconv.FormatInt(version+2, 10), seedVersion(t, f.server, key))
	require.True(t, f.admin.PasswordHash == f.administrator(t).PasswordHash, "seed must preserve the password hash")
	cliLogin(t, api.Client(), api.URL, f.password)
	t.Logf("existing session sees all four new pages and %d permissions; second seed preserves row counts %v; original password still logs in", len(current.Permissions), counts)
}

func TestSeedCLIIntegrationInvalidatesOnlyCurrentTenant(t *testing.T) {
	// FOLLOWUP-SEED §3.3: include all account states and cross the 500-user batch boundary.
	f := newSeedFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	users := make([]model.User, 501)
	for i := range users {
		users[i] = model.User{TenantID: 1, Username: fmt.Sprintf("seed-user-%d", i), PasswordHash: f.admin.PasswordHash, DepartmentID: f.admin.DepartmentID, Status: "active"}
	}
	users[0].Status, users[1].Status = "disabled", "locked"
	require.NoError(t, q.CreateInBatches(&users, 500).Error)
	require.NoError(t, q.Where("tenant_id = ? AND id = ?", 1, users[2].ID).Delete(&model.User{}).Error)
	department := model.Department{TenantID: 2, Name: "foreign", Path: "foreign", Status: "active"}
	require.NoError(t, q.Create(&department).Error)
	foreign := model.User{TenantID: 2, Username: "foreign", PasswordHash: f.admin.PasswordHash, DepartmentID: department.ID, Status: "active"}
	require.NoError(t, q.Create(&foreign).Error)
	users = append(users, f.admin)
	for _, user := range users {
		require.NoError(t, f.server.Set(authz.VersionKey(1, user.ID), "40"))
	}
	foreignKey := authz.VersionKey(2, foreign.ID)
	require.NoError(t, f.server.Set(foreignKey, "90"))
	foreignPayload := fmt.Sprintf("authz:menu:2:%d:v90", foreign.ID)
	require.NoError(t, f.server.Set(foreignPayload, "unchanged"))
	f.seed(t)
	for _, user := range users {
		require.Equal(t, "41", seedVersion(t, f.server, authz.VersionKey(1, user.ID)))
	}
	require.Equal(t, "90", seedVersion(t, f.server, foreignKey))
	require.Equal(t, "unchanged", seedVersion(t, f.server, foreignPayload))
	t.Logf("incremented %d tenant A users, including disabled/locked/deleted accounts; tenant B version remains 90", len(users))
}

func TestSeedCLIIntegrationRollbackDoesNotInvalidate(t *testing.T) {
	// FOLLOWUP-SEED §3.4: fail after catalog writes and verify the actual transaction rolls back.
	f := newSeedFixture(t)
	f.legacyCatalog(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id = ? AND code = ?", 1, identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	counts := cliSeedRowCounts(f.ctx, t, f.database)
	key := authz.VersionKey(1, f.admin.ID)
	require.NoError(t, f.server.Set(key, "40"))
	commands := f.server.CommandCount()
	var output bytes.Buffer
	require.Error(t, run(f.ctx, []string{"seed"}, &output))
	require.Equal(t, counts, cliSeedRowCounts(f.ctx, t, f.database))
	require.Equal(t, commands, f.server.CommandCount(), "failed seed must not contact Redis")
	require.Equal(t, "40", seedVersion(t, f.server, key))
	require.Empty(t, output.String())
	t.Log("seed transaction rolled back catalog writes; authorization version remains 40; no Redis commands issued")
}

func TestSeedCLIIntegrationCacheFailureWarnsWithoutFailingSeed(t *testing.T) {
	// FOLLOWUP-SEED §3.5: both connection and per-key INCR failures preserve the committed result.
	for _, failure := range []string{"unavailable", "partial increment failure"} {
		t.Run(failure, func(t *testing.T) {
			f := newSeedFixture(t)
			f.legacyCatalog(t)
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			user := model.User{TenantID: 1, Username: "other", PasswordHash: f.admin.PasswordHash, DepartmentID: f.admin.DepartmentID, Status: "active"}
			require.NoError(t, q.Create(&user).Error)
			key := authz.VersionKey(1, f.admin.ID)
			otherKey := authz.VersionKey(1, user.ID)
			require.NoError(t, f.server.Set(otherKey, "80"))
			if failure == "unavailable" {
				f.server.Close()
			} else {
				require.NoError(t, f.server.Set(key, "invalid-version"))
			}
			var output bytes.Buffer
			require.NoError(t, run(f.ctx, []string{"seed"}, &output))
			require.Contains(t, output.String(), "warning:")
			require.Contains(t, output.String(), "authorization cache invalidation failed")
			require.Contains(t, output.String(), "seed completed\n")
			require.NotContains(t, output.String(), f.password)
			require.NotContains(t, output.String(), f.admin.PasswordHash)
			counts := cliSeedRowCounts(f.ctx, t, f.database)
			require.EqualValues(t, len(permission.All()), counts[0], "catalog writes must commit")
			if failure != "unavailable" {
				require.Equal(t, "invalid-version", seedVersion(t, f.server, key))
				require.Equal(t, "81", seedVersion(t, f.server, otherKey), "a failed key must not prevent other users from being invalidated")
			}
			t.Log("seed completed with an explicit warning; the full permission catalog committed")
		})
	}
}

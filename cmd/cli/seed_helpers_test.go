package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/omwk/go-admin-api/internal/bootstrap"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

type seedFixture struct {
	ctx      context.Context
	database *db.DB
	server   *miniredis.Miniredis
	cfg      *config.Config
	admin    model.User
	password string
}

func newSeedFixture(t *testing.T) *seedFixture {
	t.Helper()
	database, ctx, dsn := cliIntegrationDatabase(t)
	_, err := database.Migrate(ctx, "up")
	require.NoError(t, err)
	password, err := refresh.Generate()
	require.NoError(t, err)
	secret, err := refresh.Generate()
	require.NoError(t, err)
	master := make([]byte, 32)
	_, err = rand.Read(master)
	require.NoError(t, err)
	f := &seedFixture{ctx: ctx, database: database, server: miniredis.RunT(t), password: password}
	for key, value := range map[string]string{
		"CH_CONFIG_PATH": "", "CH_DB_DSN": dsn, "CH_JWT_SECRET": secret,
		"CH_CRYPTO_MASTER_KEY": base64.StdEncoding.EncodeToString(master),
		"CH_REDIS_ADDR":        f.server.Addr(), "CH_REDIS_PASSWORD": "", "CH_REDIS_DB": "0",
		"CH_SEED_ADMIN_PASSWORD": password,
	} {
		t.Setenv(key, value)
	}
	f.cfg, err = config.Load("")
	require.NoError(t, err)
	f.seed(t)
	f.admin = f.administrator(t)
	return f
}

func (f *seedFixture) seed(t *testing.T) {
	t.Helper()
	var output bytes.Buffer
	require.NoError(t, run(f.ctx, []string{"seed"}, &output))
	require.Equal(t, "seed completed\n", output.String())
}

func (f *seedFixture) administrator(t *testing.T) model.User {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var user model.User
	require.NoError(t, q.Select("id, tenant_id, username, password_hash, dept_id").
		Where("tenant_id = ? AND username = ?", 1, identity.SeedAdminUsername).Take(&user).Error)
	return user
}

func (f *seedFixture) api(t *testing.T) *httptest.Server {
	t.Helper()
	shared, err := cache.Open(f.ctx, f.cfg.Redis, f.cfg.Cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, shared.Close()) })
	log, err := logger.New(io.Discard, "info", "json")
	require.NoError(t, err)
	router, err := httpx.NewEngine(httpx.Dependencies{DB: f.database, Redis: shared, Log: log})
	require.NoError(t, err)
	closeIdentity, err := bootstrap.RegisterIdentity(f.ctx, router, f.database, shared, f.cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, closeIdentity()) })
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}

func seedUpgradePaths() []string {
	return []string{"/system/notifications", "/system/notifications/channels", "/system/audit-logs", "/system/login-logs"}
}

func seedUpgradeCodes() []string {
	return []string{permission.SystemNotifyList, permission.SystemNotifyManage, permission.SystemNotifyRead, permission.SystemAuditList}
}

func (f *seedFixture) legacyCatalog(t *testing.T) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	paths := append(seedUpgradePaths(), "/system/notifications/read")
	var menus, permissions []int64
	require.NoError(t, q.Model(&model.Menu{}).Where("tenant_id = ? AND path IN ?", 1, paths).Pluck("id", &menus).Error)
	require.Len(t, menus, len(paths))
	require.NoError(t, q.Where("tenant_id = ? AND menu_id IN ?", 1, menus).Delete(&model.RoleMenu{}).Error)
	require.NoError(t, q.Where("tenant_id = ? AND id IN ?", 1, menus).Delete(&model.Menu{}).Error)
	require.NoError(t, q.Model(&model.Permission{}).Where("tenant_id = ? AND code IN ?", 1, seedUpgradeCodes()).Pluck("id", &permissions).Error)
	require.Len(t, permissions, len(seedUpgradeCodes()))
	require.NoError(t, q.Where("tenant_id = ? AND permission_id IN ?", 1, permissions).Delete(&model.RolePermission{}).Error)
	require.NoError(t, q.Where("tenant_id = ? AND id IN ?", 1, permissions).Delete(&model.Permission{}).Error)
}

func seedProfile(t *testing.T, api *httptest.Server, path, bearer string) identity.MenusResponse {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, api.URL+"/api/v1/auth/"+path, nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := api.Client().Do(request)
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var envelope struct {
		Data identity.MenusResponse `json:"data"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	return envelope.Data
}

func seedMenuPaths(nodes []identity.MenuNode) []string {
	paths := []string{}
	for _, node := range nodes {
		if node.Path != nil {
			paths = append(paths, *node.Path)
		}
		paths = append(paths, seedMenuPaths(node.Children)...)
	}
	return paths
}

func seedVersion(t *testing.T, server *miniredis.Miniredis, key string) string {
	t.Helper()
	value, err := server.Get(key)
	require.NoError(t, err)
	return value
}

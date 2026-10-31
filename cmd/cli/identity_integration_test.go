package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/idgen"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

func cliIntegrationDatabase(t *testing.T) (*db.DB, context.Context, string) {
	t.Helper()
	dsn := testenv.Lookup("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)
	cfg := config.DB{DSN: dsn, MaxOpen: 4, MaxIdle: 1, ConnMaxLifetime: time.Minute}
	admin, err := db.Open(ctx, cfg)
	require.NoError(t, err)
	id, err := idgen.NewULID()
	require.NoError(t, err)
	schema := "cli_" + strings.ToLower(id)
	require.NoError(t, admin.Exec(ctx, "CREATE SCHEMA "+schema))
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		require.NoError(t, admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"))
		require.NoError(t, admin.Close())
	})
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		cfg.DSN = parsed.String()
	} else {
		cfg.DSN += " search_path=" + schema
	}
	database, err := db.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	return database, ctx, cfg.DSN
}

func TestIdentityCLIIntegrationFreshDatabaseToMenus(t *testing.T) {
	// REQ-IAM-001/006/011: exercise actual make targets and the compiled API against a fresh schema.
	database, ctx, dsn := cliIntegrationDatabase(t)
	password, err := refresh.Generate()
	require.NoError(t, err)
	secret, err := refresh.Generate()
	require.NoError(t, err)
	masterKey := make([]byte, 32)
	_, err = rand.Read(masterKey)
	require.NoError(t, err)
	server := miniredis.RunT(t)
	for key, value := range map[string]string{"CH_CONFIG_PATH": "", "CH_DB_DSN": dsn, "CH_CRYPTO_MASTER_KEY": base64.StdEncoding.EncodeToString(masterKey), "CH_JWT_SECRET": secret, "CH_REDIS_ADDR": server.Addr(), "CH_REDIS_PASSWORD": "", "CH_REDIS_DB": "0", "CH_SEED_ADMIN_PASSWORD": password} {
		t.Setenv(key, value)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	runMake(ctx, t, root, "migrate-up")
	runMake(ctx, t, root, "seed")
	before := cliSeedRowCounts(ctx, t, database)
	runMake(ctx, t, root, "seed")
	require.Equal(t, before, cliSeedRowCounts(ctx, t, database))
	apiBinary := filepath.Join(t.TempDir(), "api")
	if os.PathSeparator == '\\' {
		apiBinary += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", apiBinary, "./cmd/api")
	build.Dir = root
	require.NoError(t, build.Run())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	t.Setenv("CH_APP_HTTP_ADDR", address)
	startAPI(ctx, t, apiBinary)
	client := &http.Client{Timeout: 30 * time.Second}
	base := "http://" + address
	require.Eventually(t, func() bool {
		response, err := client.Get(base + "/healthz")
		if err != nil {
			return false
		}
		err = response.Body.Close()
		return err == nil && response.StatusCode == 200
	}, 30*time.Second, 50*time.Millisecond)
	access := cliLogin(t, client, base, password)
	assertCLIMenus(t, client, base, access)
	runMake(ctx, t, root, "migrate-version")
}

func runMake(ctx context.Context, t *testing.T, root, target string) {
	t.Helper()
	command := exec.CommandContext(ctx, "make", target)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("make %s failed: %v", target, err)
	}
	require.NotContains(t, string(output), "password_hash")
	t.Logf("make %s: passed", target)
}

func startAPI(ctx context.Context, t *testing.T, path string) {
	t.Helper()
	processCtx, stop := context.WithCancel(ctx)
	command := exec.CommandContext(processCtx, path)
	require.NoError(t, command.Start())
	t.Cleanup(func() {
		stop()
		err := command.Wait()
		var exited *exec.ExitError
		if err != nil && !errors.As(err, &exited) {
			require.NoError(t, err)
		}
	})
}

func cliSeedRowCounts(ctx context.Context, t *testing.T, database *db.DB) []int64 {
	t.Helper()
	q, cancel := database.WithContext(ctx)
	defer cancel()
	counts := []int64{}
	for _, table := range []string{"sys_permission", "sys_menu", "sys_role", "sys_role_menu", "sys_role_permission", "sys_dept", "sys_user", "sys_user_role"} {
		var count int64
		require.NoError(t, q.Table(table).Count(&count).Error)
		counts = append(counts, count)
	}
	return counts
}

func cliLogin(t *testing.T, client *http.Client, base, password string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": "admin", "password": password})
	require.NoError(t, err)
	response, err := client.Post(base+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	require.Equal(t, 200, response.StatusCode)
	var envelope struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	require.NotEmpty(t, envelope.Data.AccessToken)
	return envelope.Data.AccessToken
}

func assertCLIMenus(t *testing.T, client *http.Client, base, access string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, base+"/api/v1/auth/menus", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+access)
	response, err := client.Do(request)
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	require.Equal(t, 200, response.StatusCode)
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	for _, key := range []string{"user", "menus", "permissions"} {
		require.NotEmpty(t, envelope.Data[key])
	}
	user := envelope.Data["user"].(map[string]any)
	require.Equal(t, "admin", user["username"])
	require.NotContains(t, user, "password_hash")
}

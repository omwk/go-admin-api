package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTenantDefaultCodeConfiguration(t *testing.T) {
	// BE-IDN-001: built-in default, file, then environment override.
	t.Setenv("CH_TENANT_DEFAULT_CODE", "restore-after-test")
	require.NoError(t, os.Unsetenv("CH_TENANT_DEFAULT_CODE"))
	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "default", cfg.Tenant.DefaultCode)
	path := filepath.Join(t.TempDir(), "tenant.yaml")
	require.NoError(t, os.WriteFile(path, []byte("tenant:\n  default_code: acme\n"), 0o600))
	cfg, err = Load(path)
	require.NoError(t, err)
	require.Equal(t, "acme", cfg.Tenant.DefaultCode)
	t.Setenv("CH_TENANT_DEFAULT_CODE", "another")
	cfg, err = Load(path)
	require.NoError(t, err)
	require.Equal(t, "another", cfg.Tenant.DefaultCode)
	for _, value := range []string{"", "   ", strings.Repeat("a", 65)} {
		t.Setenv("CH_TENANT_DEFAULT_CODE", value)
		_, err := Load(path)
		require.ErrorContains(t, err, "tenant.default_code")
	}
	t.Log("tenant.default_code: default -> file acme -> CH_TENANT_DEFAULT_CODE another; blank/oversized values rejected")
}

func TestTenantPlatformConfiguration(t *testing.T) {
	t.Setenv("CH_TENANT_PLATFORM_ID", "1")
	cfg, err := Load("")
	require.NoError(t, err)
	require.EqualValues(t, 1, cfg.Tenant.PlatformID)
	t.Setenv("CH_TENANT_PLATFORM_ID", "2")
	cfg, err = Load("")
	require.NoError(t, err)
	require.EqualValues(t, 2, cfg.Tenant.PlatformID)
}

func TestTenantPlatformDefaultFileAndInvalidConfiguration(t *testing.T) {
	previous, exists := os.LookupEnv("CH_TENANT_PLATFORM_ID")
	require.NoError(t, os.Unsetenv("CH_TENANT_PLATFORM_ID"))
	t.Cleanup(func() {
		if exists {
			require.NoError(t, os.Setenv("CH_TENANT_PLATFORM_ID", previous))
		} else {
			require.NoError(t, os.Unsetenv("CH_TENANT_PLATFORM_ID"))
		}
	})
	cfg, err := Load("")
	require.NoError(t, err)
	require.EqualValues(t, 1, cfg.Tenant.PlatformID)
	path := filepath.Join(t.TempDir(), "tenant.yaml")
	require.NoError(t, os.WriteFile(path, []byte("tenant:\n  platform_id: 2\n"), 0o600))
	cfg, err = Load(path)
	require.NoError(t, err)
	require.EqualValues(t, 2, cfg.Tenant.PlatformID)
	for _, value := range []string{"0", "-1", "invalid"} {
		t.Setenv("CH_TENANT_PLATFORM_ID", value)
		_, err := Load(path)
		require.ErrorContains(t, err, "tenant.platform_id")
	}
}

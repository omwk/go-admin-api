package config

import (
	"bytes"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGatewayDevelopmentDefault(t *testing.T) {
	// BE-IDN-032 §4: only dev receives a default, with a single warning.
	t.Setenv("CH_APP_ENV", "dev")
	t.Setenv("CH_GATEWAY_PUBLIC_BASE_URL", "")
	t.Setenv("CH_GATEWAY_ALLOWED_ORIGINS", "")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:18080", cfg.Gateway.PublicBaseURL)
	require.Empty(t, cfg.Gateway.AllowedOrigins)
	cfg.DB.DSN = "postgres://localhost/database"
	cfg.Crypto.MasterKey = generatedValue(t, 32)
	require.NoError(t, cfg.Validate())
	require.Contains(t, logs.String(), "level=WARN")
	require.Equal(t, 1, bytes.Count(logs.Bytes(), []byte("gateway.public_base_url")))
}

func TestGatewayConfigurationSources(t *testing.T) {
	for _, key := range []string{"CH_GATEWAY_PUBLIC_BASE_URL", "CH_GATEWAY_ALLOWED_ORIGINS"} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("gateway:\n  public_base_url: https://file.example\n  allowed_origins: [https://file-admin.example]\n"), 0o600))
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "https://file.example", cfg.Gateway.PublicBaseURL)
	require.Equal(t, []string{"https://file-admin.example"}, cfg.Gateway.AllowedOrigins)

	t.Setenv("CH_GATEWAY_PUBLIC_BASE_URL", "https://env.example:8443")
	t.Setenv("CH_GATEWAY_ALLOWED_ORIGINS", "https://env-admin.example,http://[::1]:3000")
	cfg, err = Load(path)
	require.NoError(t, err)
	require.Equal(t, "https://env.example:8443", cfg.Gateway.PublicBaseURL)
	require.Equal(t, []string{"https://env-admin.example", "http://[::1]:3000"}, cfg.Gateway.AllowedOrigins)
	cfg.DB.DSN = "postgres://localhost/database"
	cfg.Crypto.MasterKey = generatedValue(t, 32)
	require.NoError(t, cfg.Validate())

	t.Setenv("CH_GATEWAY_ALLOWED_ORIGINS", "")
	cfg, err = Load(path)
	require.NoError(t, err)
	require.Empty(t, cfg.Gateway.AllowedOrigins)
}

func TestGatewayRequiresOriginOutsideDevelopment(t *testing.T) {
	for _, env := range []string{"prod", "staging", ""} {
		t.Run(env, func(t *testing.T) {
			cfg := gatewayValidationConfig(t)
			cfg.App.Env = env
			cfg.Gateway.PublicBaseURL = ""
			require.ErrorContains(t, cfg.Validate(), "gateway.public_base_url is required")
		})
	}
}

func TestGatewayRejectsInvalidOriginsWithoutLoggingValues(t *testing.T) {
	credential := url.User(generatedValue(t, 12)).String()
	for _, invalid := range []string{
		"example.invalid", "ftp://example.invalid", "https://", "https://:443",
		"https://example.invalid/", "https://example.invalid/path", "https://example.invalid?x=1",
		"https://example.invalid?", "https://example.invalid#fragment", "https://example.invalid#",
		"https://" + credential + "@example.invalid", "https://*.example.invalid",
		"https://example.invalid:", "https://example.invalid:65536", "https://::1",
		"https://[example.invalid]", "null",
	} {
		cfg := gatewayValidationConfig(t)
		base := cfg.Gateway.PublicBaseURL
		cfg.Gateway.PublicBaseURL = invalid
		err := cfg.Validate()
		require.ErrorContains(t, err, "gateway.public_base_url")
		require.NotContains(t, err.Error(), invalid)

		cfg.Gateway.PublicBaseURL = base
		cfg.Gateway.AllowedOrigins = []string{"https://admin.example", invalid}
		err = cfg.Validate()
		require.ErrorContains(t, err, "gateway.allowed_origins[1]")
		require.NotContains(t, err.Error(), invalid)
	}
}

func gatewayValidationConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		App:     App{Env: "prod", PublicURL: "https://accounts.example"},
		DB:      DB{DSN: "postgres://localhost/database"},
		Crypto:  Crypto{MasterKey: generatedValue(t, 32)},
		JWT:     JWT{Secret: generatedValue(t, 32)},
		Gateway: Gateway{PublicBaseURL: "https://api.example"},
	}
}

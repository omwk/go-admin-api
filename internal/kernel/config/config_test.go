package config

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadEnvironmentOverridesFile(t *testing.T) {
	// REQ-SYS-002: environment values override the configuration file.
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte("db:\n  dsn: postgres://file.example/database\n"), 0o600)
	require.NoError(t, err)
	t.Setenv("CH_DB_DSN", "postgres://env.example/database")

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "postgres://env.example/database", cfg.DB.DSN)
}

func TestValidateNamesMissingRequiredField(t *testing.T) {
	t.Setenv("CH_DB_DSN", "")
	cfg, err := Load("")
	require.NoError(t, err)
	err = cfg.Validate()
	require.ErrorContains(t, err, "db.dsn")
}

func TestLoadDefaultsAndFileOverride(t *testing.T) {
	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, cfg.App.ReadTimeout)
	require.Equal(t, 50, cfg.DB.MaxOpen)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("db:\n  max_open: 12\n"), 0o600))
	cfg, err = Load(path)
	require.NoError(t, err)
	require.Equal(t, 12, cfg.DB.MaxOpen)
}

func TestLoadEmptyEnvironmentOverridesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("db:\n  dsn: postgres://file.example/database\n"), 0o600))
	t.Setenv("CH_DB_DSN", "")
	cfg, err := Load(path)
	require.NoError(t, err)
	require.ErrorContains(t, cfg.Validate(), "db.dsn")
}

func TestValidateValidAndInvalidSettings(t *testing.T) {
	for _, tc := range []struct {
		name, dsn, master, env, jwt, field string
	}{
		{name: "invalid port", dsn: "host=localhost port=invalid", field: "db.dsn"},
		{name: "missing key", dsn: "postgres://localhost/database", field: "crypto.master_key"},
		{name: "valid", dsn: "host=localhost dbname=database", master: generatedValue(t, 32)},
		{name: "valid prefixed", dsn: "postgres://localhost/database", master: "base64:" + generatedValue(t, 32)},
		{name: "valid prod", dsn: "postgres://localhost/database", master: generatedValue(t, 32), env: "prod", jwt: generatedValue(t, 32)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{App: App{Env: tc.env}, DB: DB{DSN: tc.dsn}, Crypto: Crypto{MasterKey: tc.master}, JWT: JWT{Secret: tc.jwt}}
			err := cfg.Validate()
			if tc.field == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.field)
			}
		})
	}
}

func TestLoadInvalidValueDoesNotExposeValue(t *testing.T) {
	value := generatedValue(t, 32)
	t.Setenv("CH_DB_MAX_OPEN", value)
	_, err := Load("")
	require.ErrorContains(t, err, "db.max_open")
	require.NotContains(t, err.Error(), value)
}

func TestValidateRequiresValidMasterKeyAndProductionJWTSecret(t *testing.T) {
	cfg, err := Load("")
	require.NoError(t, err)
	cfg.DB.DSN = "postgres://localhost/database"
	cfg.Crypto.MasterKey = generatedValue(t, 32)
	cfg.App.Env = "prod"
	cfg.JWT.Secret = generatedValue(t, 16)

	err = cfg.Validate()
	require.ErrorContains(t, err, "jwt.secret")

	cfg.Crypto.MasterKey = generatedValue(t, 16)
	cfg.JWT.Secret = ""
	err = cfg.Validate()
	require.ErrorContains(t, err, "crypto.master_key")
}

func generatedValue(t *testing.T, size int) string {
	t.Helper()
	value := make([]byte, size)
	_, err := rand.Read(value)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(value)
}

func TestValidateRejectsUnparseableDSN(t *testing.T) {
	cfg, err := Load("")
	require.NoError(t, err)
	cfg.DB.DSN = "not a postgres dsn"
	err = cfg.Validate()
	require.ErrorContains(t, err, "db.dsn")
}

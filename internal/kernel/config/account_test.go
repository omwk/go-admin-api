package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountPublicURLConfiguration(t *testing.T) {
	t.Setenv("CH_APP_PUBLIC_URL", "https://accounts.example.invalid/console")
	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "https://accounts.example.invalid/console", cfg.App.PublicURL)
	cfg.DB.DSN = "postgres://localhost/database"
	cfg.Crypto.MasterKey = generatedValue(t, 32)
	require.NoError(t, cfg.Validate())
	for _, invalid := range []string{"/relative", "javascript:bad", "https://user@host", "https://host?query=x", "https://host#fragment"} {
		cfg.App.PublicURL = invalid
		err := cfg.Validate()
		require.ErrorContains(t, err, "app.public_url")
		require.NotContains(t, err.Error(), invalid)
	}
}

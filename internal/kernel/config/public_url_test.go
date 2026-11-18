package config

import (
	"bytes"
	"log/slog"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicURLDevelopmentDefault(t *testing.T) {
	// BE-IDN-016 §4 #1: an omitted development origin is usable and visible.
	t.Setenv("CH_APP_ENV", "dev")
	t.Setenv("CH_APP_PUBLIC_URL", "")
	t.Setenv("CH_APP_NAME", "CommerceHub")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:5555", cfg.App.PublicURL)
	require.Contains(t, logs.String(), "level=WARN")
	require.Contains(t, logs.String(), "app.public_url")
}

func TestPublicURLProductionRequired(t *testing.T) {
	t.Setenv("CH_APP_ENV", "prod")
	t.Setenv("CH_APP_PUBLIC_URL", "")
	cfg, err := Load("")
	require.NoError(t, err)
	require.Empty(t, cfg.App.PublicURL)
	require.ErrorContains(t, cfg.Validate(), "app.public_url")
}

func TestPublicURLRejectsInvalidBaseWithoutExposingIt(t *testing.T) {
	for _, base := range []string{" ", "/relative", "ftp://example.invalid", "https://example.invalid?view=mail", "https://example.invalid#section", "https://" + url.User(generatedValue(t, 24)).String() + "@example.invalid"} {
		cfg := Config{App: App{Env: "prod", PublicURL: base}}
		err := cfg.Validate()
		require.ErrorContains(t, err, "app.public_url")
		if base != " " {
			require.False(t, bytes.Contains([]byte(err.Error()), []byte(base)))
		}
	}
}

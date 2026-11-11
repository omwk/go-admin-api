package channel

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/stretchr/testify/require"
)

func randomValue(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 24)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	return hex.EncodeToString(raw)
}

func TestTemplateRestrictedVariables(t *testing.T) {
	for _, source := range []string{"{{.Unknown}}", "{{printf \"%s\" .Name}}", "{{if .Name}}yes{{end}}", "{{.Name | html}}", "{{.Name.Other}}", "{{template \"x\" .}}"} {
		t.Run(source, func(t *testing.T) {
			_, err := Render(Template{Subject: "subject", Body: source, Format: "text", Enabled: true}, map[string]string{"Name": "Ada"})
			require.Error(t, err)
		})
	}
	m, err := Render(Template{Subject: "Hello {{.Name}}", Body: "<p>{{.Name}}</p>", Format: "html", Enabled: true}, map[string]string{"Name": "<script>"})
	require.NoError(t, err)
	require.Equal(t, "<p>&lt;script&gt;</p>", m.Body)
	_, err = Render(Template{Subject: "{{.Name}}", Body: "body", Format: "text", Enabled: true}, map[string]string{"Name": "bad\r\nheader"})
	require.Error(t, err)
}

func TestProviderEncryptionBindsTrustedRelationships(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	s, err := crypto.NewKeyring(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	clear(key)
	tenant := int64(1)
	p := Provider{ID: 7, TenantID: &tenant, Kind: "http"}
	secret := randomValue(t)
	raw, err := SealJSON(s, ProviderAAD(p), Config{"secret": secret})
	require.NoError(t, err)
	require.NotContains(t, string(raw), secret)
	var decoded Config
	require.NoError(t, OpenJSON(s, ProviderAAD(p), raw, &decoded))
	require.Equal(t, secret, decoded["secret"])
	for _, mutate := range []func(*Provider){func(p *Provider) { p.ID++ }, func(p *Provider) { id := int64(2); p.TenantID = &id }, func(p *Provider) { p.Kind = "smtp" }, func(p *Provider) { p.TenantID = nil }} {
		other := p
		mutate(&other)
		require.Error(t, OpenJSON(s, ProviderAAD(other), raw, &decoded))
	}
	view, err := json.Marshal(SafeConfig(Config{"secret": secret, "endpoint": "https://example.invalid/?key=" + secret, "api_key": secret}))
	require.NoError(t, err)
	require.NotContains(t, string(view), secret)
	require.Contains(t, string(view), "secret_configured")
}

func TestProviderConfigurationRejectsUnsafeInputs(t *testing.T) {
	for _, cfg := range []Config{{"endpoint": "http://example.invalid", "secret": randomValue(t)}, {"endpoint": "https://u:p@example.invalid", "secret": randomValue(t)}, {"endpoint": "https://example.invalid", "unknown": "value"}, {"endpoint": "https://example.invalid", "secret": strings.Repeat("a", 4097)}} {
		require.Error(t, ValidateConfig("webhook", "http", cfg))
	}
	require.Error(t, ValidateConfig("sms", "smtp", Config{}))
	require.Error(t, ValidateConfig("email", "smtp", Config{"smtp_addr": "example.invalid:25", "tls_mode": "plain", "from_address": randomValue(t) + "@example.invalid"}))
}

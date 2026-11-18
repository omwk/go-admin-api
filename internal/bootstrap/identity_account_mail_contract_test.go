package bootstrap

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestAccountMailAbsoluteLinkOpenAPI(t *testing.T) {
	for file, schemas := range map[string]map[string]string{
		"account.yaml":  {"AccountInvite": "link", "AccountResetLink": "link", "AccountRecoveryAccepted": "dev_reset_url"},
		"identity.yaml": {"RegistrationAccepted": "dev_verify_url"},
		"system.yaml":   {"TenantProvisionResult": "invite_url", "TenantAdminInviteResult": "invite_url"},
	} {
		raw, err := os.ReadFile("../../api/openapi/" + file)
		require.NoError(t, err)
		var doc contractMap
		require.NoError(t, yaml.Unmarshal(raw, &doc))
		definitions := doc["components"].(contractMap)["schemas"].(contractMap)
		for name, property := range schemas {
			properties := definitions[name].(contractMap)["properties"].(contractMap)
			description := properties[property].(contractMap)["description"].(string)
			require.Contains(t, strings.ToLower(description), "absolute http(s)")
			require.Contains(t, description, "tenant=<code>")
			require.Contains(t, description, "tenant_id=<id>")
			require.Contains(t, description, "fragment")
		}
	}
}

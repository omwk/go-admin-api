package bootstrap

import (
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestSeededTemplatesRender(t *testing.T) {
	// BE-IDN-016 §3.6: render every built-in with its actual producer's variable set.
	rows := seededNotificationTemplates()
	require.Len(t, rows, 14)
	for _, row := range rows {
		t.Run(row.Code, func(t *testing.T) {
			for _, support := range []string{"", "support@example.invalid"} {
				variables := seededTemplateVariables(t, row.Code, support)
				message, err := channel.Render(row, variables)
				require.NoError(t, err, "built-in %s must use provided variables only", row.Code)
				require.NotEmpty(t, strings.TrimSpace(message.Subject))
				require.NotEmpty(t, strings.TrimSpace(message.Body))
				require.Equal(t, "text", message.Format)
				if strings.HasPrefix(row.Code, "identity.") {
					require.Contains(t, message.Body, "\n\n"+variables["Link"]+"\n\n")
					require.Contains(t, message.Body, "只能使用一次")
					require.Contains(t, message.Body, variables["TenantName"])
					require.Contains(t, message.Subject, variables["AppName"])
					if support == "" {
						require.NotContains(t, message.Body, "如遇问题请联系")
					} else {
						require.Contains(t, message.Body, "如遇问题请联系："+support)
					}
				}
			}
		})
	}
}

func seededTemplateVariables(t *testing.T, code, support string) map[string]string {
	t.Helper()
	switch {
	case strings.HasPrefix(code, "identity."):
		raw, err := refresh.Generate()
		require.NoError(t, err)
		purpose := strings.TrimPrefix(code, "identity.")
		link, err := identity.AccountLink("https://accounts.example.invalid", purpose, 7, "acme", raw)
		require.NoError(t, err)
		now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
		variables := accountMailVariables("CommerceHub", purpose, link, support, accountMailData{Username: "Ada", TenantName: "Acme", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)})
		variables["TokenID"] = "1"
		return variables
	case strings.HasPrefix(code, "credential."):
		return credentialNotificationVariables(credentialAlert{Platform: "mock", Severity: "critical"})
	case code == "notification.default":
		return map[string]string{"Name": "Ada"}
	case code == "notification.test":
		return map[string]string{}
	default:
		t.Fatalf("built-in has no production variable contract: %s", code)
		return nil
	}
}

func TestAccountMailVariablesAlwaysPresent(t *testing.T) {
	now := time.Date(2026, 11, 18, 20, 0, 0, 0, time.FixedZone("local", 8*60*60))
	for _, tc := range []struct {
		purpose, lifetime string
		ttl               time.Duration
	}{{"verify", "24", 24 * time.Hour}, {"reset", "30", 30 * time.Minute}, {"invite", "168", 7 * 24 * time.Hour}} {
		data := accountMailData{Username: "Ada", TenantName: "Acme", CreatedAt: now, ExpiresAt: now.Add(tc.ttl)}
		variables := accountMailVariables("", tc.purpose, "", "", data)
		require.Len(t, variables, 7)
		for _, key := range []string{"AppName", "UserName", "TenantName", "Link", "ExpiresInHours", "ExpiresAt", "SupportEmail"} {
			require.Contains(t, variables, key)
		}
		require.Equal(t, "CommerceHub", variables["AppName"])
		require.Equal(t, "Ada", variables["UserName"])
		require.Equal(t, tc.lifetime, variables["ExpiresInHours"])
		require.Equal(t, data.ExpiresAt.UTC().Format(time.RFC3339), variables["ExpiresAt"])
		nickname := "张三"
		data.Nickname = &nickname
		variables = accountMailVariables("Custom Brand", tc.purpose, "", "support@example.invalid", data)
		require.Equal(t, nickname, variables["UserName"])
		require.Equal(t, "Custom Brand", variables["AppName"])
		require.Equal(t, "support@example.invalid", variables["SupportEmail"])
	}
}

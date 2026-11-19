package channel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmailChangeVariablesAndMandatorySecurityMail(t *testing.T) {
	for _, code := range []string{"identity.email_change", "identity.email_change_notice"} {
		require.True(t, Transactional(code))
		values := ExampleVariables(code)
		require.Len(t, values, 11)
		for _, key := range []string{"NewEmail", "RequestedAt", "IP", "UserAgent"} {
			require.Contains(t, values, key)
		}
		if code == "identity.email_change_notice" {
			require.Empty(t, values["Link"])
		}
		require.ErrorContains(t, ValidateTemplate(Template{Code: code, Format: "text", Subject: "safe", Body: "{{.MissingEmailChangeVariable}}"}), "MissingEmailChangeVariable")
		_, err := Render(Template{Enabled: true, Format: "text", Subject: "safe", Body: "{{.NewEmail}}"}, map[string]string{})
		require.Error(t, err)
	}
}

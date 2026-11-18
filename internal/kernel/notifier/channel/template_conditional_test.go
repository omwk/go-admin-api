package channel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTemplateOptionalSupportEmail(t *testing.T) {
	template := Template{Subject: "subject", Body: "body{{if .SupportEmail}}\n联系：{{.SupportEmail}}{{end}}", Format: "text", Enabled: true}
	for _, support := range []string{"", "support@example.invalid"} {
		message, err := Render(template, map[string]string{"SupportEmail": support})
		require.NoError(t, err)
		if support == "" {
			require.Equal(t, "body", message.Body)
		} else {
			require.Equal(t, "body\n联系："+support, message.Body)
		}
	}
	_, err := Render(template, map[string]string{})
	require.Error(t, err)
}

func TestTemplateSupportConditionRemainsRestricted(t *testing.T) {
	for _, body := range []string{
		"{{if .Unknown}}yes{{end}}", "{{if .SupportEmail.Other}}yes{{end}}",
		"{{if printf \"%s\" .SupportEmail}}yes{{end}}", "{{if .SupportEmail | html}}yes{{end}}",
		"{{if .SupportEmail}}{{.Unknown}}{{end}}", "{{if .SupportEmail}}yes{{else}}{{.Unknown}}{{end}}",
		"{{range .SupportEmail}}yes{{end}}", "{{with .SupportEmail}}yes{{end}}",
	} {
		_, err := Render(Template{Subject: "subject", Body: body, Format: "text", Enabled: true}, map[string]string{"SupportEmail": ""})
		require.Error(t, err)
	}
}

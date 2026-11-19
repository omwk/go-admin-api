package channel

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNotificationVariablesComplete(t *testing.T) {
	for _, code := range []string{"identity.verify", "identity.reset", "identity.invite", "notification.broadcast", "notification.default", "credential.failed"} {
		declarations := Variables(code)
		require.NotEmpty(t, declarations, code)
		for _, v := range declarations {
			require.NotEmpty(t, v.Name)
			require.NotEmpty(t, v.Description)
			require.NotEmpty(t, v.Example)
		}
		tpl := Template{Code: code, Enabled: true, Format: "text", Subject: "safe", Body: "{{.Undeclared}}"}
		err := ValidateTemplate(tpl)
		require.ErrorContains(t, err, "Undeclared")
		tpl.Subject, tpl.Body = "{{.SubjectUnknown}}", "safe"
		require.ErrorContains(t, ValidateTemplate(tpl), "SubjectUnknown")
		tpl.Subject, tpl.Body = "safe", "{{if .ConditionUnknown}}text{{end}}"
		require.ErrorContains(t, ValidateTemplate(tpl), "ConditionUnknown")
	}
	require.Empty(t, Variables("notification.test"))
	require.True(t, Transactional("identity.verify"))
	require.False(t, Transactional("notification.broadcast"))
}

func TestNotificationVariablesRejectExpressionsComplete(t *testing.T) {
	for _, source := range []string{"{{.Name | printf}}", "{{template \"x\" .}}", "{{range .Name}}{{.}}{{end}}", "{{if .SupportEmail}}{{.Unknown}}{{end}}"} {
		require.Error(t, ValidateTemplate(Template{Code: "identity.verify", Format: "text", Subject: "safe", Body: source}))
	}
	_, err := Render(Template{Enabled: true, Format: "text", Subject: "safe", Body: "{{.Name}}"}, map[string]string{})
	require.Error(t, err, "missingkey=error remains enforced")
	require.NotContains(t, strings.ToLower(err.Error()), "secret")
}

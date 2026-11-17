package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func TestAdminNotificationConfigErrorMapping(t *testing.T) {
	configError := &channel.ConfigError{Field: "smtp_addr", Reason: "SMTP 地址需包含端口，例如 smtp.163.com:465"}
	for _, source := range []error{configError, fmt.Errorf("validate provider: %w", configError)} {
		app := errs.From(providerConfigError(source))
		require.Equal(t, 10001, app.Code)
		require.Equal(t, 400, app.HTTPStatus)
		require.Equal(t, configError.Reason, app.Message)
		require.Equal(t, map[string]any{"field": "smtp_addr", "reason": configError.Reason}, app.Details)
		require.ErrorIs(t, app, source)
	}
	app := errs.From(providerConfigError(errors.New("private validation failure")))
	require.Equal(t, 10001, app.Code)
	require.Equal(t, 400, app.HTTPStatus)
	require.Equal(t, "通道配置无效", app.Message)
	require.Nil(t, app.Details)
}

func TestAdminNotificationConfigOpenAPI(t *testing.T) {
	// BE-IDN-014 §3.10: only provider writes use the field-diagnostic contract.
	doc := readSystemOpenAPI(t)
	paths := doc["paths"].(contractMap)
	responses := doc["components"].(contractMap)["responses"].(contractMap)
	media := responses["NotificationProvider400"].(contractMap)["content"].(contractMap)["application/json"].(contractMap)
	schema := media["schema"].(contractMap)["allOf"].([]any)[1].(contractMap)
	details := schema["properties"].(contractMap)["details"].(contractMap)
	require.ElementsMatch(t, []any{"field", "reason"}, details["required"])
	for _, field := range []string{"field", "reason"} {
		require.Equal(t, "string", details["properties"].(contractMap)[field].(contractMap)["type"])
	}
	example, err := json.Marshal(media["example"])
	require.NoError(t, err)
	for _, tc := range []struct{ path, method string }{
		{"/api/v1/admin/notifications/providers", "post"},
		{"/api/v1/admin/notifications/providers/{id}", "put"},
	} {
		response := paths[tc.path].(contractMap)[tc.method].(contractMap)["responses"].(contractMap)["400"].(contractMap)
		require.Equal(t, "#/components/responses/NotificationProvider400", response["$ref"])
		validateOpenAPIResponse(t, doc, tc.path, tc.method, 400, example)
		validateOpenAPIResponse(t, doc, tc.path, tc.method, 400, []byte(`{"code":10001,"message":"通道配置无效","trace_id":"example"}`))
	}
	test := paths["/api/v1/admin/notifications/providers/{id}/test"].(contractMap)["post"].(contractMap)["responses"].(contractMap)["400"].(contractMap)
	require.Equal(t, "#/components/responses/Notification400", test["$ref"])
}

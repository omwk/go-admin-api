package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func deliveryContractProperties(t *testing.T, doc, schema contractMap) contractMap {
	t.Helper()
	schema = resolveContract(t, doc, schema)
	out := contractMap{}
	if properties, ok := schema["properties"].(contractMap); ok {
		for key, value := range properties {
			out[key] = value
		}
	}
	if all, ok := schema["allOf"].([]any); ok {
		for _, part := range all {
			for key, value := range deliveryContractProperties(t, doc, part.(contractMap)) {
				out[key] = value
			}
		}
	}
	return out
}

func TestSystemNotificationDeliveryOpenAPIReadability(t *testing.T) {
	// BE-IDN-015 §3 #12: compare response fields in both directions, including null masks.
	doc := readSystemOpenAPI(t)
	schemas := doc["components"].(contractMap)["schemas"].(contractMap)
	for name, value := range map[string]any{
		"NotificationDelivery": channel.Delivery{Status: "pending", Channel: "email"},
		"NotificationDeliveryView": channel.DeliveryView{
			Delivery:     channel.Delivery{Status: "failed", Channel: "sms", LastError: "message_unavailable"},
			TemplateName: "系统通知", LastErrorMessage: "消息内容已过期或模板已删除",
		},
		"NotificationDeliveryRetryResult": channel.RetryBatchResult{Retried: 2, Skipped: 1},
	} {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		var actual contractMap
		require.NoError(t, json.Unmarshal(raw, &actual))
		schema := schemas[name].(contractMap)
		validateContract(t, doc, schema, actual)
		properties := deliveryContractProperties(t, doc, schema)
		require.Len(t, actual, len(properties), name)
		for key := range actual {
			require.Contains(t, properties, key, name)
		}
	}
	operation := doc["paths"].(contractMap)["/api/v1/admin/notifications/deliveries"].(contractMap)["get"].(contractMap)
	parameters := contractMap{}
	for _, raw := range operation["parameters"].([]any) {
		param := raw.(contractMap)
		parameters[param["name"].(string)] = param["schema"]
	}
	require.Len(t, parameters, 5)
	for _, name := range []string{"page", "page_size", "status", "channel", "template_code"} {
		require.Contains(t, parameters, name)
	}
	ids := schemas["NotificationDeliveryRetryRequest"].(contractMap)["properties"].(contractMap)["ids"].(contractMap)
	require.EqualValues(t, 1, ids["minItems"])
	require.EqualValues(t, 100, ids["maxItems"])
	require.Equal(t, true, ids["uniqueItems"])
	t.Log("§3 #12: Delivery/View/RetryResult fields match in both directions; nullable mask; all five query parameters; batch bounds 1..100 unique IDs")
}

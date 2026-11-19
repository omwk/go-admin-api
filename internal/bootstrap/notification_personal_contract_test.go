package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func TestSystemNotificationRoutingOpenAPI(t *testing.T) {
	// BE-IDN-019 §3 #12: compare personal input/output fields in both directions.
	doc := readSystemOpenAPI(t)
	schemas := doc["components"].(contractMap)["schemas"].(contractMap)
	enabled := true
	for name, value := range map[string]any{
		"NotificationPersonalChannel":        channel.PersonalChannelView{Channel: "bark", EndpointMasked: "https://example.invalid/…/****"},
		"NotificationPersonalChannelRequest": channel.PersonalChannelInput{Endpoint: "https://example.invalid/push", Enabled: &enabled},
	} {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		var actual contractMap
		require.NoError(t, json.Unmarshal(raw, &actual))
		properties := deliveryContractProperties(t, doc, schemas[name].(contractMap))
		require.Len(t, actual, len(properties), name)
		for key := range actual {
			require.Contains(t, properties, key, name)
		}
	}
	view := schemas["NotificationPersonalChannel"].(contractMap)["properties"].(contractMap)
	require.NotContains(t, view, "endpoint")
	input := schemas["NotificationPersonalChannelRequest"].(contractMap)["properties"].(contractMap)["endpoint"].(contractMap)
	require.EqualValues(t, 2048, input["maxLength"])
	require.Equal(t, true, input["writeOnly"])
	preferences := schemas["NotificationPreferenceView"].(contractMap)["properties"].(contractMap)
	require.Contains(t, preferences, "available")
	require.Contains(t, preferences, "reason")
	t.Log("#12: personal input/output JSON fields match OpenAPI both ways; endpoint writeOnly/maxLength=2048 and absent from response; preference available/reason documented")
}

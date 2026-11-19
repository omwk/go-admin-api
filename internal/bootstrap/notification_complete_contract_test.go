package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func TestSystemNotificationOpenAPIComplete(t *testing.T) {
	doc := readSystemOpenAPI(t)
	schemas := doc["components"].(contractMap)["schemas"].(contractMap)
	for name, value := range map[string]any{
		"NotificationVariable":         channel.Variables("identity.verify")[0],
		"NotificationTemplatePreview":  channel.TemplatePreview{Subject: "preview", Body: "body"},
		"NotificationBroadcastRequest": channel.BroadcastInput{Title: "title", Body: "body", Channels: []string{"inbox"}, Audience: channel.Audience{Type: "users", IDs: []int64{1}}},
		"NotificationBroadcastResult":  channel.BroadcastResult{BroadcastID: 1, Recipients: 1},
		"NotificationBroadcast":        channel.BroadcastView{BroadcastResult: channel.BroadcastResult{BroadcastID: 1}, Channels: []string{"inbox"}, Audience: channel.Audience{Type: "all"}},
		"NotificationPreferenceView":   notifier.PreferenceView{Template: "identity.verify", Name: "验证注册邮箱", Transactional: true, Channel: "email", Enabled: true},
		"NotificationInbox":            credentialNotification{},
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
	t.Log("`3 #14: new variable, preview, broadcast, preference and inbox JSON fields match OpenAPI in both directions; HTTP tests validate actual values and statuses")
}

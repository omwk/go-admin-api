package setting

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

func TestRegistryDefaultsAndValidation(t *testing.T) {
	registry := DefaultRegistry()
	require.Len(t, registry.Groups(), 5)
	count := 0
	for _, group := range registry.Groups() {
		items, ok := registry.Items(group)
		require.True(t, ok)
		count += len(items)
		for _, item := range items {
			body, err := json.Marshal(item.Default)
			require.NoError(t, err)
			_, err = registry.Validate(group, map[string]json.RawMessage{item.Key: body})
			require.NoError(t, err)
		}
	}
	require.Equal(t, 17, count)
	for _, tc := range []struct {
		group, key, value string
		valid             bool
	}{
		{"security", "password_min_length", `"abc"`, false},
		{"security", "password_min_length", `3`, false},
		{"security", "password_min_length", `8`, true},
		{"security", "password_min_length", `64`, true},
		{"security", "password_min_length", `65`, false},
		{"security", "password_min_length", `8.5`, false},
		{"security", "password_require_mixed", `false`, true},
		{"security", "password_require_mixed", `"true"`, false},
		{"security", "password_expire_days", `0`, true},
		{"security", "password_history_size", `21`, false},
		{"security", "unknown", `1`, false},
		{"system", "system_name", `""`, false},
		{"system", "timezone", `"Asia/Shanghai"`, true},
		{"system", "timezone", `"invalid/zone"`, false},
		{"notification", "default_channel", `"webhook"`, true},
		{"notification", "default_channel", `"unknown"`, false},
		{"notification", "retry_max_attempts", `null`, false},
	} {
		t.Run(tc.group+"/"+tc.key+"/"+tc.value, func(t *testing.T) {
			_, err := registry.Validate(tc.group, map[string]json.RawMessage{tc.key: json.RawMessage(tc.value)})
			if tc.valid {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Equal(t, 400, errs.From(err).HTTPStatus)
			require.Equal(t, tc.key, errs.From(err).Details["key"])
		})
	}
}

func TestRegistryReadOnlyAndOwnedSchemas(t *testing.T) {
	items, _ := DefaultRegistry().Items("system")
	items[0].ReadOnly = true
	registry, err := NewRegistry(items)
	require.NoError(t, err)
	body, err := json.Marshal(items[0].Default)
	require.NoError(t, err)
	_, err = registry.Validate("system", map[string]json.RawMessage{items[0].Key: body})
	require.Equal(t, 400, errs.From(err).HTTPStatus)
	items[0].Key = "changed"
	owned, _ := registry.Items("system")
	require.NotEqual(t, "changed", owned[0].Key)
}

func TestSyncRegistryValidation(t *testing.T) {
	// BE-8 section 4.3-5: reuse S2 type, enum, integer and range validation.
	for _, tc := range []struct {
		key, value string
		valid      bool
	}{
		{"unknown_value_policy", "\"reject\"", true},
		{"unknown_value_policy", "\"keep_raw\"", true},
		{"unknown_value_policy", "\"fallback_null\"", true},
		{"unknown_value_policy", "\"drop\"", false},
		{"unknown_value_policy", "true", false},
		{"drift_alert_threshold", "0", false},
		{"drift_alert_threshold", "1", true},
		{"drift_alert_threshold", "10000", true},
		{"drift_alert_threshold", "10001", false},
		{"drift_alert_threshold", "1.5", false},
		{"drift_alert_threshold", "\"1\"", false},
		{"auto_promote_baseline", "false", true},
		{"auto_promote_baseline", "true", true},
		{"auto_promote_baseline", "\"yes\"", false},
		{"auto_promote_baseline", "1", false},
		{"auto_promote_baseline", "null", false},
		{"unknown", "1", false},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			_, err := DefaultRegistry().Validate("sync", map[string]json.RawMessage{tc.key: json.RawMessage(tc.value)})
			if tc.valid {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Equal(t, 400, errs.From(err).HTTPStatus)
			require.Equal(t, "sync", errs.From(err).Details["group"])
			require.Equal(t, tc.key, errs.From(err).Details["key"])
		})
	}
}

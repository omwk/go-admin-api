package setting

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

func TestRegistryDefaultsAndValidation(t *testing.T) {
	registry := DefaultRegistry()
	require.Len(t, registry.Groups(), 3)
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
	require.Equal(t, 11, count)
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

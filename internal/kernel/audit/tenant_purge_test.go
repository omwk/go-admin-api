package audit

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTenantPurgeAuditPreservesOnlyIntegerCounts(t *testing.T) {
	event := Event{Action: "system.tenant.purge", ResourceType: "tenant",
		After: map[string]any{"code": "archived", "rows": map[string]int64{"auth_token": 2, "sys_refresh_token": 1}}}
	encoded, err := marshalAfter(event)
	require.NoError(t, err)
	require.JSONEq(t, `{"code":"archived","rows":{"auth_token":2,"sys_refresh_token":1}}`, string(encoded))
	event.Action = "system.tenant.archive"
	encoded, err = marshalAfter(event)
	require.NoError(t, err)
	require.JSONEq(t, `{"code":"archived","rows":{"auth_token":"***","sys_refresh_token":"***"}}`, string(encoded))
	event.Action = "system.tenant.purge"
	for _, rows := range []any{map[string]any{"auth_token": "private"}, map[string]int64{"auth_token": -1}, nil} {
		event.After["rows"] = rows
		_, err = marshalAfter(event)
		require.Error(t, err)
	}
}

package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestSystemSyncSettingsIntegrationDefaults(t *testing.T) {
	// BE-8 §4.1/2: an unset tenant sees four complete groups and the safe sync defaults.
	f := newSystemFixture(t, nil)
	response, envelope := systemRequest(t, f, "GET", "/admin/settings", nil, "")
	require.Equal(t, 200, response.Code)
	var groups []system.SettingGroup
	require.NoError(t, json.Unmarshal(envelope.Data, &groups))
	byGroup := map[string]system.SettingGroup{}
	names := []string{}
	for _, group := range groups {
		byGroup[group.Group] = group
		names = append(names, group.Group)
	}
	require.Len(t, groups, 4)
	require.ElementsMatch(t, []string{"security", "system", "notification", "sync"}, names)
	require.Len(t, byGroup["sync"].Items, 3)
	values := map[string]any{}
	for _, item := range byGroup["sync"].Items {
		values[item.Schema.Key] = item.Value
		require.Equal(t, "sync", item.Schema.Group)
		require.Equal(t, item.Schema.Default, item.Value)
		require.False(t, item.Schema.ReadOnly)
		require.False(t, item.Schema.HotReload)
		switch item.Schema.Key {
		case "unknown_value_policy":
			require.Equal(t, "string", item.Schema.Type)
			require.Equal(t, []string{"reject", "keep_raw", "fallback_null"}, item.Schema.Enum)
		case "drift_alert_threshold":
			require.Equal(t, "number", item.Schema.Type)
			require.NotNil(t, item.Schema.Min)
			require.NotNil(t, item.Schema.Max)
			require.Equal(t, float64(1), *item.Schema.Min)
			require.Equal(t, float64(10000), *item.Schema.Max)
		case "auto_promote_baseline":
			require.Equal(t, "boolean", item.Schema.Type)
		}
	}
	require.Equal(t, map[string]any{"unknown_value_policy": "keep_raw", "drift_alert_threshold": float64(1), "auto_promote_baseline": false}, values)
	t.Log("GET /admin/settings: security/system/notification/sync; sync defaults=keep_raw/1/false; enum and bounds exposed")
}

func TestSystemSyncSettingsIntegrationValidation(t *testing.T) {
	// BE-8 §4.3-5: HTTP identifies the rejected key and preserves atomic S2 validation.
	f := newSystemFixture(t, nil)
	for _, tc := range []struct {
		name, key string
		value     any
		status    int
	}{
		{"invalid_enum", "unknown_value_policy", "drop", 400},
		{"below_min", "drift_alert_threshold", 0, 400},
		{"above_max", "drift_alert_threshold", 10001, 400},
		{"fraction", "drift_alert_threshold", 1.5, 400},
		{"number_string", "drift_alert_threshold", "1", 400},
		{"minimum", "drift_alert_threshold", 1, 200},
		{"maximum", "drift_alert_threshold", 10000, 200},
		{"boolean_string", "auto_promote_baseline", "yes", 400},
		{"boolean_null", "auto_promote_baseline", nil, 400},
		{"unregistered", "unknown", true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, envelope := systemRequest(t, f, "PUT", "/admin/settings/sync", map[string]any{tc.key: tc.value}, "")
			require.Equal(t, tc.status, response.Code)
			if tc.status == 400 {
				require.Equal(t, errs.ErrInvalidParam, envelope.Code)
				require.Equal(t, "sync", envelope.Details["group"])
				require.Equal(t, tc.key, envelope.Details["key"])
			}
			t.Logf("%s=%v -> %d", tc.key, tc.value, response.Code)
		})
	}
	response, _ := systemRequest(t, f, "PUT", "/admin/settings/sync", map[string]any{"auto_promote_baseline": true, "unknown_value_policy": "drop"}, "")
	require.Equal(t, 400, response.Code)
	response, envelope := systemRequest(t, f, "GET", "/admin/settings/sync", nil, "")
	require.Equal(t, 200, response.Code)
	require.Equal(t, false, systemGroupValue(t, envelope.Data, "auto_promote_baseline"))
	require.Equal(t, "keep_raw", systemGroupValue(t, envelope.Data, "unknown_value_policy"))
	t.Log("invalid mixed batch leaves both registered values unchanged")
}

func TestSystemSyncSettingsIntegrationUpdateAndAudit(t *testing.T) {
	// BE-8 §4.6/7: each PUT writes one S2 event with all keys and before/after digests.
	f := newSystemFixture(t, nil)
	before := map[string]any{"unknown_value_policy": "keep_raw", "drift_alert_threshold": 1, "auto_promote_baseline": false}
	updates := map[string]any{"unknown_value_policy": "reject", "drift_alert_threshold": 7, "auto_promote_baseline": true}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for i := range 2 {
		response, envelope := systemRequest(t, f, "PUT", "/admin/settings/sync", updates, "")
		require.Equal(t, 200, response.Code)
		for key, value := range updates {
			require.EqualValues(t, value, systemGroupValue(t, envelope.Data, key))
		}
		response, envelope = systemRequest(t, f, "GET", "/admin/settings/sync", nil, "")
		require.Equal(t, 200, response.Code)
		for key, value := range updates {
			require.EqualValues(t, value, systemGroupValue(t, envelope.Data, key))
		}
		var rows []struct {
			Before, After json.RawMessage
			ActorID       int64
			Result        string
		}
		require.NoError(t, q.Table("audit_log").Select("\"before\",\"after\",actor_id,result").Where("tenant_id=? AND action=? AND resource_type=? AND resource_id=?", 1, "system:setting:update", "setting", "sync").Order("id").Find(&rows).Error)
		require.Len(t, rows, i+1)
		require.Equal(t, f.actor.User.ID, rows[i].ActorID)
		require.Equal(t, "success", rows[i].Result)
		assertSyncSettingAudit(t, rows[i].Before, before)
		assertSyncSettingAudit(t, rows[i].After, updates)
		before = updates
	}
	t.Log("two PUTs -> two audit rows; GET=reject/7/true; each event contains all three keys and matching before/after SHA256 digests")
}

func assertSyncSettingAudit(t *testing.T, raw json.RawMessage, values map[string]any) {
	t.Helper()
	var state struct {
		Group string
		Items map[string]struct{ Type, Digest string }
	}
	require.NoError(t, json.Unmarshal(raw, &state))
	require.Equal(t, "sync", state.Group)
	require.Len(t, state.Items, len(values))
	for key, value := range values {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		digest := sha256.Sum256(encoded)
		require.Equal(t, hex.EncodeToString(digest[:]), state.Items[key].Digest, key)
		require.NotEmpty(t, state.Items[key].Type, key)
	}
}

func TestSystemSyncSettingsIntegrationTenantIsolation(t *testing.T) {
	// BE-8 §4.8: changing tenant A preserves both defaults and stored overrides for B.
	f := newSystemFixture(t, nil)
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'sync_settings_other','Other tenant')"))
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO sys_setting(tenant_id,group_key,item_key,value) VALUES(2,'sync','drift_alert_threshold','42')"))
	before, err := f.settings.Values(f.ctx, 2, "sync")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"unknown_value_policy": "keep_raw", "drift_alert_threshold": float64(42), "auto_promote_baseline": false}, before)
	response, _ := systemRequest(t, f, "PUT", "/admin/settings/sync", map[string]any{"unknown_value_policy": "fallback_null", "drift_alert_threshold": 7, "auto_promote_baseline": true}, "")
	require.Equal(t, 200, response.Code)
	after, err := f.settings.Values(f.ctx, 2, "sync")
	require.NoError(t, err)
	require.Equal(t, before, after)
	response, envelope := systemRequest(t, f, "GET", "/admin/settings/sync?tenant_id=2", nil, "")
	require.Equal(t, 200, response.Code)
	require.EqualValues(t, 7, systemGroupValue(t, envelope.Data, "drift_alert_threshold"))
	response, envelope = systemRequest(t, f, "PUT", "/admin/settings/sync", map[string]any{"tenant_id": 2, "drift_alert_threshold": 9}, "")
	require.Equal(t, 400, response.Code)
	require.Equal(t, "tenant_id", envelope.Details["key"])
	t.Log("tenant A=fallback_null/7/true; tenant B remains keep_raw/42/false; query tenant_id cannot select B and body tenant_id is rejected")
}

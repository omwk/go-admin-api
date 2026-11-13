package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestSystemSettingsIntegrationAcceptance(t *testing.T) {
	// S2 §5.1-5: schema completeness, current values and atomic registered validation.
	f := newSystemFixture(t, nil)
	response, envelope := systemRequest(t, f, "GET", "/admin/settings", nil, "")
	require.Equal(t, 200, response.Code)
	var groups []system.SettingGroup
	require.NoError(t, json.Unmarshal(envelope.Data, &groups))
	require.Len(t, groups, 4)
	count := 0
	for _, group := range groups {
		for _, item := range group.Items {
			count++
			require.NotEmpty(t, item.Schema.Key)
			require.Equal(t, item.Schema.Default, item.Value)
		}
	}
	require.Equal(t, 14, count)
	response, envelope = systemRequest(t, f, "PUT", "/admin/settings/security", map[string]any{"password_min_length": 12}, "settings-once")
	require.Equal(t, 200, response.Code)
	require.EqualValues(t, 12, systemGroupValue(t, envelope.Data, "password_min_length"))
	replay, again := systemRequest(t, f, "PUT", "/admin/settings/security", map[string]any{"password_min_length": 12}, "settings-once")
	require.Equal(t, 200, replay.Code)
	require.JSONEq(t, string(envelope.Data), string(again.Data))
	response, _ = systemRequest(t, f, "PUT", "/admin/settings/security", map[string]any{"password_min_length": 13}, "settings-once")
	require.Equal(t, 409, response.Code)
	for _, tc := range []struct {
		key   string
		value any
	}{{"password_min_length", "abc"}, {"password_min_length", 3}, {"password_min_length", 8.5}, {"unknown", 1}, {"password_require_mixed", nil}} {
		response, envelope = systemRequest(t, f, "PUT", "/admin/settings/security", map[string]any{tc.key: tc.value}, "")
		require.Equal(t, 400, response.Code)
		require.Equal(t, tc.key, envelope.Details["key"])
	}
	response, envelope = systemRequest(t, f, "GET", "/admin/settings/security", nil, "")
	require.Equal(t, 200, response.Code)
	require.EqualValues(t, 12, systemGroupValue(t, envelope.Data, "password_min_length"))
	response, _ = systemRequest(t, f, "PUT", "/admin/settings/security", map[string]any{"password_min_length": 8}, "")
	require.Equal(t, 200, response.Code)
	response, _ = systemRequest(t, f, "PUT", "/admin/settings/security", map[string]any{"password_min_length": 16, "login_max_failures": 2}, "")
	require.Equal(t, 400, response.Code)
	_, envelope = systemRequest(t, f, "GET", "/admin/settings/security", nil, "")
	require.EqualValues(t, 8, systemGroupValue(t, envelope.Data, "password_min_length"))
	response, _ = systemRequest(t, f, "GET", "/admin/settings/missing", nil, "")
	require.Equal(t, 404, response.Code)
}

func TestSystemSettingsIntegrationReadOnlyAndAuditRollback(t *testing.T) {
	// S2 §5.6/7: the read-only flag is exercised through HTTP without adding a schema.
	var all []setting.Item
	for _, group := range setting.DefaultRegistry().Groups() {
		items, _ := setting.DefaultRegistry().Items(group)
		for i := range items {
			if items[i].Key == "system_name" {
				items[i].ReadOnly = true
			}
		}
		all = append(all, items...)
	}
	registry, err := setting.NewRegistry(all)
	require.NoError(t, err)
	f := newSystemFixture(t, registry)
	response, envelope := systemRequest(t, f, "PUT", "/admin/settings/system", map[string]any{"system_name": "Changed"}, "")
	require.Equal(t, 400, response.Code)
	require.Equal(t, "system_name", envelope.Details["key"])
	response, envelope = systemRequest(t, f, "GET", "/admin/settings/system", nil, "")
	require.Equal(t, 200, response.Code)
	require.Equal(t, "CommerceHub", systemGroupValue(t, envelope.Data, "system_name"))
	service, err := system.NewSettingService(&systemSettingStore{database: f.database}, setting.DefaultRegistry(), func(context.Context, audit.Event) error { return errors.New("injected audit failure") })
	require.NoError(t, err)
	_, err = service.Update(f.ctx, system.Actor{TenantID: 1, UserID: f.actor.User.ID}, "security", map[string]json.RawMessage{"password_min_length": json.RawMessage("32")})
	require.Error(t, err)
	_, envelope = systemRequest(t, f, "GET", "/admin/settings/security", nil, "")
	require.EqualValues(t, 8, systemGroupValue(t, envelope.Data, "password_min_length"))
}

func TestSystemSettingsIntegrationSafeAuditAndLivePageSize(t *testing.T) {
	// S2 §5.7: summaries identify before/after values without copying arbitrary string values.
	f := newSystemFixture(t, nil)
	name, err := refresh.Generate()
	require.NoError(t, err)
	response, _ := systemRequest(t, f, "PUT", "/admin/settings/system", map[string]any{"system_name": name, "default_page_size": 30}, "audit-once")
	require.Equal(t, 200, response.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []struct{ Before, After json.RawMessage }
	require.NoError(t, q.Table("audit_log").Select("\"before\",\"after\"").Where("tenant_id=? AND action=?", 1, "system:setting:update").Find(&rows).Error)
	require.Len(t, rows, 1)
	for _, state := range []json.RawMessage{rows[0].Before, rows[0].After} {
		require.Contains(t, string(state), "system_name")
		require.Contains(t, string(state), "digest")
		require.NotContains(t, string(state), name)
	}
	response, envelope := systemRequest(t, f, "GET", "/admin/dicts", nil, "")
	require.Equal(t, 200, response.Code)
	var page system.Page[system.Dict]
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.Equal(t, 30, page.PageSize)
	response, envelope = systemRequest(t, f, "GET", "/admin/dicts?page_size=10", nil, "")
	require.Equal(t, 200, response.Code)
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.Equal(t, 10, page.PageSize)
	response, envelope = adminRequest(t, f.adminFixture, "GET", "/users", nil, "")
	require.Equal(t, 200, response.Code)
	var userPage map[string]any
	require.NoError(t, json.Unmarshal(envelope.Data, &userPage))
	require.EqualValues(t, 30, userPage["page_size"])
}

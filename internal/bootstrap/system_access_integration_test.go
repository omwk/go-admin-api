package bootstrap

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestSystemDictIntegrationTenantIsolation(t *testing.T) {
	// S2 §5.13: every foreign parent/item read or write is indistinguishable from missing data.
	f := newSystemFixture(t, nil)
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,?,?)", "system-other", "Other tenant"))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	dict := system.Dict{TenantID: 2, Type: "other_only", Name: "Other dictionary", Status: "active"}
	require.NoError(t, q.Create(&dict).Error)
	item := system.DictItem{TenantID: 2, DictID: dict.ID, Label: "Other item", Value: "other", Status: "active", Extra: json.RawMessage("{}")}
	require.NoError(t, q.Create(&item).Error)
	path := "/admin/dicts/" + strconv.FormatInt(dict.ID, 10)
	itemPath := "/admin/dict-items/" + strconv.FormatInt(item.ID, 10)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", path + "/items", nil},
		{"PUT", path, system.DictRequest{Type: dict.Type, Name: "Changed", Status: "active"}},
		{"DELETE", path, nil},
		{"POST", path + "/items", system.DictItemRequest{Label: "New", Value: "new", Status: "active", Extra: json.RawMessage("{}")}},
		{"PUT", itemPath, system.DictItemRequest{Label: "Changed", Value: item.Value, Status: "active", Extra: json.RawMessage("{}")}},
		{"DELETE", itemPath, nil},
		{"GET", "/meta/dicts/other_only", nil},
	} {
		response, envelope := systemRequest(t, f, tc.method, tc.path, tc.body, "")
		require.Equal(t, 404, response.Code, "%s %s", tc.method, tc.path)
		require.Equal(t, errs.ErrNotFound, envelope.Code)
	}
	response, envelope := systemRequest(t, f, "GET", "/admin/dicts", nil, "")
	require.Equal(t, 200, response.Code)
	require.NotContains(t, string(envelope.Data), "other_only")
	stored, err := newSystemDictStore(f.database).DictionaryItem(f.ctx, 2, item.ID)
	require.NoError(t, err)
	require.Equal(t, item.Label, stored.Label)
}

func TestSystemSettingsIntegrationTenantOverrides(t *testing.T) {
	// S2 §5.13: tenant overrides cannot be selected or overwritten with client ownership fields.
	f := newSystemFixture(t, nil)
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,?,?)", "settings-other", "Other tenant"))
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO sys_setting(tenant_id,group_key,item_key,value) VALUES(2,'security','password_min_length','60')"))
	_, envelope := systemRequest(t, f, "GET", "/admin/settings/security?tenant_id=2", nil, "")
	require.EqualValues(t, 8, systemGroupValue(t, envelope.Data, "password_min_length"))
	response, _ := systemRequest(t, f, "PUT", "/admin/settings/security", map[string]any{"password_min_length": 12}, "")
	require.Equal(t, 200, response.Code)
	values, err := f.settings.Values(f.ctx, 2, "security")
	require.NoError(t, err)
	require.EqualValues(t, 60, values["password_min_length"])
	response, envelope = systemRequest(t, f, "PUT", "/admin/settings/security", map[string]any{"tenant_id": 2, "password_min_length": 16}, "")
	require.Equal(t, 400, response.Code)
	require.Equal(t, "tenant_id", envelope.Details["key"])
	_, envelope = systemRequest(t, f, "GET", "/admin/settings/security", nil, "")
	require.EqualValues(t, 12, systemGroupValue(t, envelope.Data, "password_min_length"))
}

func TestSystemMetadataIntegrationErrorCodesAndPermissions(t *testing.T) {
	// S2 §5.12: the HTTP catalog is exactly the existing numeric-code registry.
	f := newSystemFixture(t, nil)
	response, envelope := systemRequest(t, f, "GET", "/meta/error-codes", nil, "")
	require.Equal(t, 200, response.Code)
	var codes []errs.Definition
	require.NoError(t, json.Unmarshal(envelope.Data, &codes))
	require.Equal(t, errs.All(), codes)
	require.Len(t, codes, 13)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET is_super=false WHERE tenant_id=1 AND id=?", f.actor.User.ID))
	require.NoError(t, f.database.Exec(f.ctx, "DELETE FROM sys_user_role WHERE tenant_id=1 AND user_id=?", f.actor.User.ID))
	replace := strings.NewReplacer(":group", "security", ":id", "1")
	for _, route := range system.Routes() {
		if route.Permission == "" {
			continue
		}
		response, _ = systemRequest(t, f, route.Method, replace.Replace(route.Path), nil, "")
		require.Equal(t, 403, response.Code, route.Method+" "+route.Path)
	}
	for _, path := range []string{"/meta/error-codes", "/meta/dicts/order_status"} {
		response, _ = systemRequest(t, f, "GET", path, nil, "")
		require.Equal(t, 200, response.Code)
	}
	f.bearer = ""
	for _, path := range []string{"/meta/error-codes", "/meta/dicts/order_status"} {
		response, _ = systemRequest(t, f, "GET", path, nil, "")
		require.Equal(t, 401, response.Code)
	}
}

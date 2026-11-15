package bootstrap

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestSystemDictSupplyIntegrationAdminValues(t *testing.T) {
	// BE-IDN-008 §3.1/5/11: reset exposes the canonical values through unchanged admin routes.
	f := newSystemFixture(t, nil)
	_, err := ResetDictionaries(f.ctx, f.database)
	require.NoError(t, err)
	response, envelope := systemRequest(t, f, "GET", "/admin/dicts?page_size=100", nil, "")
	require.Equal(t, 200, response.Code)
	var page system.Page[system.Dict]
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 9, page.Total)
	for _, dict := range page.List {
		require.Equal(t, "system", dict.Scope)
		response, envelope := systemRequest(t, f, "GET", "/admin/dicts/"+strconv.FormatInt(dict.ID, 10)+"/items?page_size=100", nil, "")
		require.Equal(t, 200, response.Code)
		var items system.Page[system.DictItem]
		require.NoError(t, json.Unmarshal(envelope.Data, &items))
		values := []string{}
		for i, item := range items.List {
			values = append(values, item.Value)
			require.NotEmpty(t, item.Label)
			require.NotEmpty(t, item.Color)
			require.Equal(t, i+1, item.Sort)
		}
		require.Equal(t, dictSupplyValues()[dict.Type], values)
		t.Logf("GET /admin/dicts/%d/items: %s scope=system items=%d values=%v", dict.ID, dict.Type, items.Total, values)
	}
}

func TestSystemDictSupplyIntegrationRejectReadonlyFields(t *testing.T) {
	// BE-IDN-008 §3.8: public decoding also rejects ownership and future fields explicitly.
	f := newSystemFixture(t, nil)
	store := newSystemDictStore(f.database)
	dict, err := store.DictionaryByType(f.ctx, 1, "product_status")
	require.NoError(t, err)
	items, err := store.DictionaryItems(f.ctx, 1, dict.ID, 1, 100, false)
	require.NoError(t, err)
	item := items.List[0]
	dictPath := "/admin/dicts/" + strconv.FormatInt(dict.ID, 10)
	itemPath := "/admin/dict-items/" + strconv.FormatInt(item.ID, 10)
	for _, tc := range []struct {
		name, path string
		base       map[string]any
		forbidden  map[string]any
	}{
		{"dictionary", dictPath, map[string]any{"type": dict.Type, "name": dict.Name, "status": dict.Status}, map[string]any{"type": "changed", "value": "changed", "scope": "business", "builtin": false, "future": "changed"}},
		{"item", itemPath, map[string]any{"label": item.Label, "value": item.Value, "sort": item.Sort, "color": item.Color, "status": item.Status, "extra": item.Extra}, map[string]any{"type": "changed", "value": "changed", "scope": "business", "builtin": false, "future": "changed"}},
	} {
		for field, value := range tc.forbidden {
			t.Run(tc.name+"/"+field, func(t *testing.T) {
				body := map[string]any{}
				for key, value := range tc.base {
					body[key] = value
				}
				body[field] = value
				response, _ := systemRequest(t, f, "PUT", tc.path, body, "")
				status := 400
				if (tc.name == "dictionary" && field == "type") || (tc.name == "item" && field == "value") {
					status = 409
				}
				require.Equal(t, status, response.Code)
				t.Logf("PUT %s field=%s rejected HTTP %d", tc.path, field, response.Code)
			})
		}
	}
	response, _ := systemRequest(t, f, "POST", dictPath+"/items", system.DictItemRequest{Label: "Extra", Value: "extra", Status: "active"}, "")
	require.Equal(t, 400, response.Code)
	for _, path := range []string{dictPath, itemPath} {
		response, _ = systemRequest(t, f, "DELETE", path, nil, "")
		require.Equal(t, 409, response.Code)
	}
	current, err := store.DictionaryByType(f.ctx, 1, dict.Type)
	require.NoError(t, err)
	require.Equal(t, dict, current)
	currentItem, err := store.DictionaryItem(f.ctx, 1, item.ID)
	require.NoError(t, err)
	require.Equal(t, item, currentItem)
}

func TestSystemDictSupplyIntegrationDisabledUserStatusManagement(t *testing.T) {
	// BE-IDN-008 §3.9 backend portion: maintenance stays available without any meta dictionary.
	f := newSystemFixture(t, nil)
	store := newSystemDictStore(f.database)
	dict, err := store.DictionaryByType(f.ctx, 1, "user_status")
	require.NoError(t, err)
	response, _ := systemRequest(t, f, "GET", "/meta/dicts/user_status", nil, "")
	require.Equal(t, 200, response.Code)
	path := "/admin/dicts/" + strconv.FormatInt(dict.ID, 10)
	response, _ = systemRequest(t, f, "PUT", path, system.DictRequest{Type: dict.Type, Name: dict.Name, Status: "disabled"}, "")
	require.Equal(t, 200, response.Code)
	response, envelope := systemRequest(t, f, "GET", "/meta/dicts/user_status", nil, "")
	require.Equal(t, 200, response.Code)
	require.JSONEq(t, "[]", string(envelope.Data))
	response, _ = systemRequest(t, f, "GET", "/admin/dicts", nil, "")
	require.Equal(t, 200, response.Code)
	response, envelope = systemRequest(t, f, "GET", path+"/items", nil, "")
	require.Equal(t, 200, response.Code)
	var items system.Page[system.DictItem]
	require.NoError(t, json.Unmarshal(envelope.Data, &items))
	item := items.List[0]
	response, _ = systemRequest(t, f, "PUT", "/admin/dict-items/"+strconv.FormatInt(item.ID, 10), system.DictItemRequest{Label: "Maintenance label", Value: item.Value, Sort: item.Sort, Color: "blue", Status: "active", Extra: item.Extra}, "")
	require.Equal(t, 200, response.Code)
	response, _ = systemRequest(t, f, "PUT", path, system.DictRequest{Type: dict.Type, Name: dict.Name, Status: "active"}, "")
	require.Equal(t, 200, response.Code)
	t.Log("user_status disabled: meta=[]; admin list/items/edit item/re-enable all HTTP 200; frontend local fallback is outside this backend delivery")
}

package bootstrap

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestSystemDictIntegrationCRUDAndUniqueness(t *testing.T) {
	// S2 §5.8/9: cover every dictionary management operation and physical deletion constraints.
	f := newSystemFixture(t, nil)
	r := system.DictRequest{Type: "delivery_mode", Name: "配送方式", Status: "active"}
	response, envelope := systemRequest(t, f, "POST", "/admin/dicts", r, "create-dict")
	require.Equal(t, 201, response.Code)
	var dict system.Dict
	require.NoError(t, json.Unmarshal(envelope.Data, &dict))
	path := "/admin/dicts/" + strconv.FormatInt(dict.ID, 10)
	response, _ = systemRequest(t, f, "POST", "/admin/dicts", r, "")
	require.Equal(t, 409, response.Code)
	itemRequest := system.DictItemRequest{Label: "快递", Value: "courier", Sort: 2, Status: "active", Extra: json.RawMessage("{}")}
	response, envelope = systemRequest(t, f, "POST", path+"/items", itemRequest, "")
	require.Equal(t, 201, response.Code)
	var item system.DictItem
	require.NoError(t, json.Unmarshal(envelope.Data, &item))
	itemPath := "/admin/dict-items/" + strconv.FormatInt(item.ID, 10)
	response, _ = systemRequest(t, f, "POST", path+"/items", itemRequest, "")
	require.Equal(t, 409, response.Code)
	response, _ = systemRequest(t, f, "DELETE", path, nil, "")
	require.Equal(t, 409, response.Code)
	itemRequest.Label, itemRequest.Sort = "自定义快递", 1
	response, _ = systemRequest(t, f, "PUT", itemPath, itemRequest, "")
	require.Equal(t, 200, response.Code)
	r.Name = "配送方式更新"
	response, _ = systemRequest(t, f, "PUT", path, r, "")
	require.Equal(t, 200, response.Code)
	response, envelope = systemRequest(t, f, "GET", path+"/items", nil, "")
	require.Equal(t, 200, response.Code)
	var page system.Page[system.DictItem]
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, itemRequest.Label, page.List[0].Label)
	response, _ = systemRequest(t, f, "DELETE", itemPath, nil, "")
	require.Equal(t, 204, response.Code)
	response, _ = systemRequest(t, f, "DELETE", path, nil, "")
	require.Equal(t, 204, response.Code)
	response, _ = systemRequest(t, f, "DELETE", path, nil, "")
	require.Equal(t, 404, response.Code)
}

func TestSystemDictIntegrationBuiltinMetadataAndSeeds(t *testing.T) {
	// S2 §5.10/11: builtin type and item protections, editable labels, deterministic dropdown order.
	f := newSystemFixture(t, nil)
	response, envelope := systemRequest(t, f, "GET", "/meta/dicts/order_status", nil, "")
	require.Equal(t, 200, response.Code)
	require.Equal(t, "private, max-age=60", response.Header().Get("Cache-Control"))
	var meta []system.MetaItem
	require.NoError(t, json.Unmarshal(envelope.Data, &meta))
	require.Len(t, meta, 6)
	require.Equal(t, []string{"pending_payment", "paid", "shipped", "completed", "cancelled", "refunded"}, metaValues(meta))
	store := newSystemDictStore(f.database)
	dict, err := store.DictionaryByType(f.ctx, 1, "order_status")
	require.NoError(t, err)
	path := "/admin/dicts/" + strconv.FormatInt(dict.ID, 10)
	response, _ = systemRequest(t, f, "DELETE", path, nil, "")
	require.Equal(t, 409, response.Code)
	items, err := store.DictionaryItems(f.ctx, 1, dict.ID, 1, 20, false)
	require.NoError(t, err)
	row := items.List[0]
	itemPath := "/admin/dict-items/" + strconv.FormatInt(row.ID, 10)
	response, _ = systemRequest(t, f, "DELETE", itemPath, nil, "")
	require.Equal(t, 409, response.Code)
	request := system.DictItemRequest{Label: "待支付", Value: row.Value, Sort: 30, Status: "active", Extra: json.RawMessage("{}")}
	response, _ = systemRequest(t, f, "PUT", itemPath, request, "")
	require.Equal(t, 200, response.Code)
	_, envelope = systemRequest(t, f, "GET", "/meta/dicts/order_status", nil, "")
	require.NoError(t, json.Unmarshal(envelope.Data, &meta))
	require.Len(t, meta, 6)
	require.Equal(t, "待支付", meta[5].Label)
	request.Value = "changed"
	response, _ = systemRequest(t, f, "PUT", itemPath, request, "")
	require.Equal(t, 409, response.Code)
	require.NoError(t, seedSystemDictionaries(f.ctx, f.database, 1))
	updated, err := store.DictionaryItem(f.ctx, 1, row.ID)
	require.NoError(t, err)
	require.Equal(t, "待支付", updated.Label)
	request.Value, request.Status = row.Value, "disabled"
	response, _ = systemRequest(t, f, "PUT", itemPath, request, "")
	require.Equal(t, 200, response.Code)
	_, envelope = systemRequest(t, f, "GET", "/meta/dicts/order_status", nil, "")
	require.NoError(t, json.Unmarshal(envelope.Data, &meta))
	require.Len(t, meta, 5)
	for kind, count := range map[string]int{"user_status": 3, "sync_run_status": 4} {
		_, envelope = systemRequest(t, f, "GET", "/meta/dicts/"+kind, nil, "")
		require.NoError(t, json.Unmarshal(envelope.Data, &meta))
		require.Len(t, meta, count)
	}
	f.bearer = ""
	response, _ = systemRequest(t, f, "GET", "/meta/dicts/order_status", nil, "")
	require.Equal(t, 401, response.Code)
}

func metaValues(items []system.MetaItem) []string {
	values := []string{}
	for _, item := range items {
		values = append(values, item.Value)
	}
	return values
}

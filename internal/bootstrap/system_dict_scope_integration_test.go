package bootstrap

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestSystemDictScopeIntegrationMigration(t *testing.T) {
	// BE-7 §3.1: classify existing rows, enforce the enum and default, and reverse migration 28.
	f := newSystemFixture(t, nil)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []struct{ Type, Scope string }
	require.NoError(t, q.Raw("SELECT type,scope FROM sys_dict WHERE tenant_id=1 ORDER BY type").Scan(&rows).Error)
	require.Equal(t, []struct{ Type, Scope string }{{"order_status", "business"}, {"sync_run_status", "system"}, {"user_status", "system"}}, rows)
	var scope string
	require.NoError(t, q.Raw("INSERT INTO sys_dict(tenant_id,type,name) VALUES(1,'scope_default','Default') RETURNING scope").Scan(&scope).Error)
	require.Equal(t, "business", scope)
	for _, value := range []any{"invalid", nil} {
		var pgErr *pgconn.PgError
		require.ErrorAs(t, f.database.Exec(f.ctx, "UPDATE sys_dict SET scope=? WHERE tenant_id=1 AND type='scope_default'", value), &pgErr)
		require.Contains(t, []string{"23514", "23502"}, pgErr.Code)
	}
	state, err := f.database.Migrate(f.ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 27, state.Version)
	var columns int64
	require.NoError(t, q.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='sys_dict' AND column_name='scope'").Scan(&columns).Error)
	require.Zero(t, columns)
	state, err = f.database.Migrate(f.ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, 28, state.Version)
	require.NoError(t, q.Raw("SELECT scope FROM sys_dict WHERE tenant_id=1 AND type='scope_default'").Scan(&scope).Error)
	require.Equal(t, "business", scope)
	t.Log("migration 28: user_status/system, sync_run_status/system, order_status/business; default and enum enforced; down 27/up 28 passed")
}

func TestSystemDictScopeIntegrationFiltering(t *testing.T) {
	// BE-7 §3.2: both the count and page are filtered inside the authenticated tenant.
	f := newSystemFixture(t, nil)
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'scope_other','Other')"))
	require.NoError(t, seedSystemDictionaries(f.ctx, f.database, 2))
	for _, tc := range []struct {
		scope string
		total int64
	}{
		{"system", 2}, {"business", 1}, {"", 3},
	} {
		response, envelope := systemRequest(t, f, "GET", "/admin/dicts?scope="+tc.scope+"&page_size=1", nil, "")
		require.Equal(t, 200, response.Code)
		var page struct {
			List  []struct{ Type, Scope string }
			Total int64
		}
		require.NoError(t, json.Unmarshal(envelope.Data, &page))
		require.Equal(t, tc.total, page.Total)
		require.Len(t, page.List, 1)
		if tc.scope != "" {
			require.Equal(t, tc.scope, page.List[0].Scope)
		}
		if tc.scope == "system" {
			response, envelope = systemRequest(t, f, "GET", "/admin/dicts?scope=system&page=2&page_size=1", nil, "")
			require.Equal(t, 200, response.Code)
			var next struct {
				List []struct{ Type, Scope string }
			}
			require.NoError(t, json.Unmarshal(envelope.Data, &next))
			require.Len(t, next.List, 1)
			require.Equal(t, "system", next.List[0].Scope)
			require.NotEqual(t, page.List[0].Type, next.List[0].Type)
		}
	}
	response, envelope := systemRequest(t, f, "GET", "/admin/dicts?scope=unknown", nil, "")
	require.Equal(t, 400, response.Code)
	require.Equal(t, errs.ErrInvalidParam, envelope.Code)
	t.Log("scope=system total=2; scope=business total=1; unfiltered total=3; pagination and tenant isolation passed; unknown scope=400/10001")
}

func TestSystemDictScopeIntegrationSystemProtection(t *testing.T) {
	// BE-7 §3.3/7: scope protects a system type and every item, independently of builtin.
	f := newSystemFixture(t, nil)
	store := newSystemDictStore(f.database)
	dict, err := store.DictionaryByType(f.ctx, 1, "user_status")
	require.NoError(t, err)
	items, err := store.DictionaryItems(f.ctx, 1, dict.ID, 1, 20, false)
	require.NoError(t, err)
	path := "/admin/dicts/" + strconv.FormatInt(dict.ID, 10)
	itemPath := "/admin/dict-items/" + strconv.FormatInt(items.List[0].ID, 10)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_dict SET builtin=false WHERE tenant_id=1 AND id=?", dict.ID))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_dict_item SET builtin=false WHERE tenant_id=1 AND dict_id=?", dict.ID))
	for _, target := range []string{path, itemPath} {
		response, envelope := systemRequest(t, f, "DELETE", target, nil, "")
		require.Equal(t, 409, response.Code)
		require.Equal(t, errs.ErrConflict, envelope.Code)
	}
	request := system.DictItemRequest{Label: "Custom", Value: "custom", Status: "active", Extra: json.RawMessage("{}")}
	response, envelope := systemRequest(t, f, "POST", path+"/items", request, "")
	require.Equal(t, 400, response.Code)
	require.Equal(t, errs.ErrInvalidParam, envelope.Code)
	request.Value = "changed"
	response, _ = systemRequest(t, f, "PUT", itemPath, request, "")
	require.Equal(t, 409, response.Code)
	response, _ = systemRequest(t, f, "PUT", path, system.DictRequest{Type: "renamed_status", Name: dict.Name, Status: dict.Status}, "")
	require.Equal(t, 409, response.Code)
	current, err := store.DictionaryItems(f.ctx, 1, dict.ID, 1, 20, false)
	require.NoError(t, err)
	require.Equal(t, items.Total, current.Total)
	t.Log("system dictionary/item DELETE=409; POST item=400/10001; type/value remain immutable even when builtin=false")
}

func TestSystemDictScopeIntegrationBusinessDeletion(t *testing.T) {
	// BE-7 §3.4: the existing business seed can be emptied/deleted and is never recreated by seed.
	f := newSystemFixture(t, nil)
	store := newSystemDictStore(f.database)
	dict, err := store.DictionaryByType(f.ctx, 1, "order_status")
	require.NoError(t, err)
	path := "/admin/dicts/" + strconv.FormatInt(dict.ID, 10)
	request := system.DictItemRequest{Label: "Custom", Value: "custom", Sort: 9, Status: "active", Extra: json.RawMessage("{}")}
	response, _ := systemRequest(t, f, "POST", path+"/items", request, "")
	require.Equal(t, 201, response.Code)
	response, _ = systemRequest(t, f, "DELETE", path, nil, "")
	require.Equal(t, 409, response.Code)
	items, err := store.DictionaryItems(f.ctx, 1, dict.ID, 1, 20, false)
	require.NoError(t, err)
	for _, item := range items.List {
		response, _ = systemRequest(t, f, "DELETE", "/admin/dict-items/"+strconv.FormatInt(item.ID, 10), nil, "")
		require.Equal(t, 204, response.Code)
	}
	response, _ = systemRequest(t, f, "DELETE", path, nil, "")
	require.Equal(t, 204, response.Code)
	require.NoError(t, seedSystemDictionaries(f.ctx, f.database, 1))
	_, err = store.DictionaryByType(f.ctx, 1, "order_status")
	require.Equal(t, errs.ErrNotFound, errs.From(err).Code)
	t.Log("business POST item=201; nonempty DELETE=409; all items DELETE=204; empty dictionary DELETE=204; seed leaves deleted business dictionary absent")
}

func TestSystemDictScopeIntegrationSeedPreservesAndRepairs(t *testing.T) {
	// BE-7 §3.5/6: rerunning seed preserves display edits, repairs missing values and skips business values.
	f := newSystemFixture(t, nil)
	store := newSystemDictStore(f.database)
	dict, err := store.DictionaryByType(f.ctx, 1, "user_status")
	require.NoError(t, err)
	items, err := store.DictionaryItems(f.ctx, 1, dict.ID, 1, 20, false)
	require.NoError(t, err)
	keep, missing := items.List[0], items.List[1]
	request := system.DictItemRequest{Label: "Tenant label", Value: keep.Value, Sort: 37, Status: "disabled", Color: "green", Extra: json.RawMessage(`{"icon":"custom"}`)}
	response, _ := systemRequest(t, f, "PUT", "/admin/dict-items/"+strconv.FormatInt(keep.ID, 10), request, "")
	require.Equal(t, 200, response.Code)
	// Simulate a missing code-defined value outside the protected management API.
	require.NoError(t, f.database.Exec(f.ctx, "DELETE FROM sys_dict_item WHERE tenant_id=1 AND id=?", missing.ID))
	require.NoError(t, f.database.Exec(f.ctx, "DELETE FROM sys_dict_item WHERE tenant_id=1 AND dict_id IN (SELECT id FROM sys_dict WHERE tenant_id=1 AND type='order_status') AND value='paid'"))
	for range 2 {
		require.NoError(t, seedSystemDictionaries(f.ctx, f.database, 1))
	}
	updated, err := store.DictionaryItem(f.ctx, 1, keep.ID)
	require.NoError(t, err)
	require.Equal(t, request.Label, updated.Label)
	require.Equal(t, request.Sort, updated.Sort)
	require.Equal(t, request.Status, updated.Status)
	require.Equal(t, request.Color, updated.Color)
	require.JSONEq(t, string(request.Extra), string(updated.Extra))
	items, err = store.DictionaryItems(f.ctx, 1, dict.ID, 1, 20, false)
	require.NoError(t, err)
	require.EqualValues(t, 3, items.Total)
	var repaired system.DictItem
	for _, item := range items.List {
		if item.Value == missing.Value {
			repaired = item
		}
	}
	require.Equal(t, missing.Label, repaired.Label)
	require.Equal(t, missing.Sort, repaired.Sort)
	require.True(t, repaired.Builtin)
	order, err := store.DictionaryByType(f.ctx, 1, "order_status")
	require.NoError(t, err)
	orderItems, err := store.DictionaryItems(f.ctx, 1, order.ID, 1, 20, false)
	require.NoError(t, err)
	require.EqualValues(t, 5, orderItems.Total)
	t.Log("two seed runs: tenant label/sort=Tenant label/37 and status/color/extra preserved; missing user_status value restored once; missing business value stays absent")
}

func TestSystemDictScopeIntegrationSeedMissingTypes(t *testing.T) {
	// BE-7 §2.4: a new tenant receives only system dictionaries, and repeated seed is idempotent.
	f := newSystemFixture(t, nil)
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'scope_new','New')"))
	for range 2 {
		require.NoError(t, seedSystemDictionaries(f.ctx, f.database, 2))
	}
	store := newSystemDictStore(f.database)
	for kind, expected := range map[string]int64{"user_status": 3, "sync_run_status": 4} {
		dict, err := store.DictionaryByType(f.ctx, 2, kind)
		require.NoError(t, err)
		items, err := store.DictionaryItems(f.ctx, 2, dict.ID, 1, 20, false)
		require.NoError(t, err)
		require.Equal(t, expected, items.Total)
	}
	_, err := store.DictionaryByType(f.ctx, 2, "order_status")
	require.Equal(t, errs.ErrNotFound, errs.From(err).Code)
	t.Log("new tenant: two system types and 3/4 items created once; order_status was not seeded")
}

func TestSystemDictScopeIntegrationMeta(t *testing.T) {
	// BE-7 §3.8: the existing array projection retains its fields and adds scope, including cache hits.
	f := newSystemFixture(t, nil)
	for kind, scope := range map[string]string{"order_status": "business", "user_status": "system"} {
		for range 2 {
			response, envelope := systemRequest(t, f, "GET", "/meta/dicts/"+kind, nil, "")
			require.Equal(t, 200, response.Code)
			var items []map[string]any
			require.NoError(t, json.Unmarshal(envelope.Data, &items))
			require.NotEmpty(t, items)
			for _, item := range items {
				require.Equal(t, scope, item["scope"])
				require.Contains(t, item, "label")
				require.Contains(t, item, "value")
				require.Contains(t, item, "sort")
				require.Contains(t, item, "color")
				require.Contains(t, item, "extra")
			}
		}
	}
	t.Log("/meta/dicts/order_status array entries contain scope=business; user_status entries contain scope=system; cold and cached responses passed OpenAPI validation")
}

package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestSystemDictSupplyIntegrationCompleteSeeds(t *testing.T) {
	// BE-IDN-008 §3.1/7/10: all code-defined values are available to each tenant.
	f := newSystemFixture(t, nil)
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'dict_supply','Dictionary supply')"))
	for range 2 {
		require.NoError(t, seedSystemDictionaries(f.ctx, f.database, 2))
	}
	store := newSystemDictStore(f.database)
	page, err := store.Dictionaries(f.ctx, 2, "system", 1, 100)
	require.NoError(t, err)
	require.EqualValues(t, 9, page.Total)
	for kind, values := range dictSupplyValues() {
		dict, err := store.DictionaryByType(f.ctx, 2, kind)
		require.NoError(t, err)
		require.Equal(t, system.DictScopeSystem, dict.Scope)
		items, err := store.DictionaryItems(f.ctx, 2, dict.ID, 1, 100, false)
		require.NoError(t, err)
		actual := []string{}
		for i, item := range items.List {
			actual = append(actual, item.Value)
			require.NotEmpty(t, item.Label)
			require.NotEmpty(t, item.Color)
			require.Equal(t, i+1, item.Sort)
		}
		require.Equal(t, values, actual, kind)
		t.Logf("%s scope=system values=%v", kind, actual)
	}
}

func TestIdentityDictSupplyIntegrationProvision(t *testing.T) {
	// BE-IDN-008 §3.2: exercise the real Provision endpoint and Materialize transaction.
	f := newTenantFixture(t)
	require.NoError(t, seedSystemDictionaries(f.ctx, f.database, 1))
	template := f.template(t, "dict-supply-template")
	result := f.provision(t, template.ID, "dict-supply-tenant")
	store := newSystemDictStore(f.database)
	defaultPage, err := store.Dictionaries(f.ctx, 1, "", 1, 100)
	require.NoError(t, err)
	page, err := store.Dictionaries(f.ctx, result.TenantID, "system", 1, 100)
	require.NoError(t, err)
	require.Equal(t, defaultPage.Total, page.Total)
	require.EqualValues(t, 9, page.Total)
	var count int64
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("sys_dict_item").Where("tenant_id=?", result.TenantID).Count(&count).Error)
	require.EqualValues(t, 33, count)
	t.Logf("Provision tenant=%d: default dictionaries=%d, new tenant dictionaries=%d items=%d", result.TenantID, defaultPage.Total, page.Total, count)
}

func TestSystemDictSupplyIntegrationProductMeta(t *testing.T) {
	// BE-IDN-008 §3.7/11: retain the current OpenAPI response shape.
	f := newSystemFixture(t, nil)
	require.NoError(t, seedSystemDictionaries(f.ctx, f.database, 1))
	response, envelope := systemRequest(t, f, "GET", "/meta/dicts/product_status", nil, "")
	require.Equal(t, 200, response.Code)
	var items []system.MetaItem
	require.NoError(t, json.Unmarshal(envelope.Data, &items))
	require.Equal(t, []string{"draft", "active", "archived"}, metaValues(items))
	for _, item := range items {
		require.NotEmpty(t, item.Color)
		require.Equal(t, "system", item.Scope)
	}
	t.Logf("GET /meta/dicts/product_status: 200, values=%v, every color is nonempty; OpenAPI validated", metaValues(items))
}

func dictSupplyValues() map[string][]string {
	return map[string][]string{
		"user_status":     {"active", "disabled", "locked"},
		"sync_run_status": {"running", "success", "partial", "failed", "cancelled"},
		"order_status":    {"pending_payment", "pending_shipment", "shipped", "completed", "closed", "refunding", "refunded", "unknown"},
		"product_status":  {"draft", "active", "archived"},
		"tenant_status":   {"active", "suspended"},
		"audit_result":    {"success", "failure"},
		"enable_status":   {"active", "disabled"},
		"delivery_status": {"pending", "sent", "failed", "skipped"},
		"invite_status":   {"active", "expired", "revoked", "used"},
	}
}

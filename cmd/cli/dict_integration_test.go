package main

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestSystemDictCLIIntegrationResetAllTenants(t *testing.T) {
	// BE-IDN-008 §3.3/5: reset includes all existing tenants, including suspended ones.
	f := newSeedFixture(t)
	t.Setenv("CH_APP_ENV", "dev")
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO tenant(id,code,name,status) OVERRIDING SYSTEM VALUE
		SELECT n,'dict-'||n,'Dictionary tenant '||n,CASE WHEN n=14 THEN 'suspended' ELSE 'active' END FROM generate_series(2,14) n`))
	var out bytes.Buffer
	require.NoError(t, run(f.ctx, []string{"dict-reset"}, &out))
	for tenant := 2; tenant <= 14; tenant++ {
		require.Contains(t, out.String(), fmt.Sprintf("tenant=%d dicts=0->9 items=0->33\n", tenant))
	}
	require.Contains(t, out.String(), "dict-reset completed tenants=14")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for tenant := 1; tenant <= 14; tenant++ {
		var dicts, items int64
		require.NoError(t, q.Model(&system.Dict{}).Where("tenant_id=? AND scope='system'", tenant).Count(&dicts).Error)
		require.NoError(t, q.Model(&system.DictItem{}).Where("tenant_id=?", tenant).Count(&items).Error)
		require.EqualValues(t, 9, dicts)
		require.EqualValues(t, 33, items)
	}
	var stale int64
	require.NoError(t, q.Raw(`SELECT count(*) FROM sys_dict_item i JOIN sys_dict d ON d.tenant_id=i.tenant_id AND d.id=i.dict_id
		WHERE (d.type='sync_run_status' AND i.value IN ('pending','succeeded'))
		OR (d.type='order_status' AND i.value IN ('paid','cancelled'))`).Scan(&stale).Error)
	require.Zero(t, stale)
	t.Log("\n" + out.String())
	out.Reset()
	require.NoError(t, run(f.ctx, []string{"dict-reset"}, &out))
	for tenant := 1; tenant <= 14; tenant++ {
		require.Contains(t, out.String(), fmt.Sprintf("tenant=%d dicts=9->9 items=33->33\n", tenant))
	}
	t.Log("second dict-reset: all 14 tenants remain 9 dictionaries/33 items; obsolete order and sync values absent")
}

func TestSystemDictCLIIntegrationBackfillPreservesBusinessEdits(t *testing.T) {
	// BE-IDN-008 §3.6/10: seed plus backfill repairs supply without overwriting tenant data.
	f := newSeedFixture(t)
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'backfill-tenant','Backfill tenant')"))
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO sys_dict(tenant_id,type,name) VALUES(1,'delivery_mode','Custom delivery')"))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO sys_dict_item(tenant_id,dict_id,label,value,sort,color)
		SELECT tenant_id,id,'Tenant business label','courier',42,'purple' FROM sys_dict WHERE tenant_id=1 AND type='delivery_mode'`))
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE sys_dict_item SET label='Tenant system label',sort=37,color='purple',status='disabled',extra='{"icon":"custom"}'
		WHERE tenant_id=1 AND dict_id IN (SELECT id FROM sys_dict WHERE tenant_id=1 AND type='product_status') AND value='active'`))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var before []system.DictItem
	require.NoError(t, q.Select("id,tenant_id,dict_id,label,value,sort,status,color,extra,builtin").Where("tenant_id=1").Order("id").Find(&before).Error)
	var first string
	for range 2 {
		f.seed(t)
		var out bytes.Buffer
		require.NoError(t, run(f.ctx, []string{"dict-backfill"}, &out))
		if first == "" {
			first = out.String()
		} else {
			require.Contains(t, out.String(), "tenant=2 dicts=9->9 items=33->33")
		}
		var after []system.DictItem
		require.NoError(t, q.Select("id,tenant_id,dict_id,label,value,sort,status,color,extra,builtin").Where("tenant_id=1").Order("id").Find(&after).Error)
		require.Equal(t, before, after)
	}
	require.Contains(t, first, "tenant=2 dicts=0->9 items=0->33")
	t.Log("two seed + dict-backfill runs: business label/sort/color and system label/sort/color/status/extra unchanged; tenant 2 counts 0/0 -> 9/33 -> 9/33")
}

func TestSystemDictCLIIntegrationResetRollsBackFailedTenant(t *testing.T) {
	// BE-IDN-008 §2.1: a failed reseed restores the deleted types and items in the same transaction.
	f := newSeedFixture(t)
	t.Setenv("CH_APP_ENV", "dev")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var beforeDicts []system.Dict
	var beforeItems []system.DictItem
	require.NoError(t, q.Select("id,tenant_id,type,name,status,scope,builtin,created_at,updated_at").Order("id").Find(&beforeDicts).Error)
	require.NoError(t, q.Select("id,tenant_id,dict_id,label,value,sort,status,color,extra,builtin").Order("id").Find(&beforeItems).Error)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_dictionary_seed() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected dictionary seed failure'; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER reject_dictionary_seed BEFORE INSERT ON sys_dict_item
		FOR EACH ROW WHEN (NEW.value='skipped') EXECUTE FUNCTION reject_dictionary_seed()`))
	var out bytes.Buffer
	err := run(f.ctx, []string{"dict-reset"}, &out)
	require.ErrorContains(t, err, "dict-reset failed")
	require.NotContains(t, err.Error(), "injected dictionary seed failure")
	require.Empty(t, out.String())
	var afterDicts []system.Dict
	var afterItems []system.DictItem
	require.NoError(t, q.Select("id,tenant_id,type,name,status,scope,builtin,created_at,updated_at").Order("id").Find(&afterDicts).Error)
	require.NoError(t, q.Select("id,tenant_id,dict_id,label,value,sort,status,color,extra,builtin").Order("id").Find(&afterItems).Error)
	require.Equal(t, beforeDicts, afterDicts)
	require.Equal(t, beforeItems, afterItems)
	t.Log("injected failure after dictionary deletion: all type/item IDs and display fields restored; no completion output or raw database error")
}

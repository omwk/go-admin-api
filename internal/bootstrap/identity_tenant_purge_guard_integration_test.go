package bootstrap

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIdentityTenantPurgeIntegrationManifestGuard(t *testing.T) {
	// BE-IDN-011 §4.10: inspect migrated schema, not a manually maintained table inventory.
	f := newTenantFixture(t)
	actual := tenantScopedTables(t, f)
	require.NoError(t, validateTenantPurgeManifest(actual))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("CREATE TABLE tenant_purge_unregistered (tenant_id bigint NOT NULL)").Error)
	err := validateTenantPurgeManifest(tenantScopedTables(t, f))
	require.EqualError(t, err, "tenant table missing purge registration: tenant_purge_unregistered")
	require.NoError(t, q.Exec("DROP TABLE tenant_purge_unregistered").Error)
	require.NoError(t, validateTenantPurgeManifest(tenantScopedTables(t, f)))
	t.Logf("schema scan: %d tenant_id parent tables registered; new unregistered table rejected; partition children covered by parent DELETE/check", len(actual))
}

func tenantScopedTables(t *testing.T, f *tenantFixture) []string {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var tables []string
	require.NoError(t, q.Raw(`SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_attribute a ON a.attrelid=c.oid AND a.attname='tenant_id' AND NOT a.attisdropped
 WHERE n.nspname=current_schema() AND c.relkind IN ('r','p') AND NOT c.relispartition ORDER BY c.relname`).Scan(&tables).Error)
	return tables
}

func validateTenantPurgeManifest(actual []string) error {
	registered := map[string]bool{}
	deletions := tenantPurgeTables()
	if len(deletions) == 0 || deletions[len(deletions)-1] != "tenant" {
		return fmt.Errorf("tenant must be last in purge manifest")
	}
	for _, table := range deletions {
		if registered[table] {
			return fmt.Errorf("duplicate tenant purge registration: %s", table)
		}
		registered[table] = true
	}
	for table, exemption := range tenantPurgeExemptions() {
		if registered[table] || exemption.reason == "" || exemption.retain && table != "audit_log" {
			return fmt.Errorf("invalid tenant purge exemption: %s", table)
		}
		registered[table] = true
	}
	for _, table := range actual {
		if !registered[table] {
			return fmt.Errorf("tenant table missing purge registration: %s", table)
		}
	}
	for table := range registered {
		if table != "tenant" && !slices.Contains(actual, table) {
			return fmt.Errorf("stale tenant purge registration: %s", table)
		}
	}
	return nil
}

func TestIdentityTenantPurgeIntegrationBusinessDataBlocks(t *testing.T) {
	// Approved option B: block before any destructive statement, including financial evidence.
	for _, tc := range []struct{ name, sql string }{
		{"category", `INSERT INTO category(tenant_id,name,path,level) VALUES(?,'purge-blocker','1',1)`},
		{"gateway", `INSERT INTO api_route(tenant_id,code,name,capability,method,path,route_group,created_by) VALUES(?,'purge-blocker','fixture','fixture','GET','/open/v1/fixture','fixture',1)`},
		{"wallet", `INSERT INTO wallet(tenant_id,app_id) VALUES(?,'purge-blocker')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTenantFixture(t)
			result := f.provision(t, f.template(t, "business-block").ID, "business-block")
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			require.NoError(t, q.Exec(tc.sql, result.TenantID).Error)
			purgeGrace(t, f, 0)
			archiveTenantForPurge(t, f, result.TenantID)
			before := tenantPurgeFingerprint(t, f, result.TenantID)
			w, _ := f.request(t, "DELETE", tenantPath(result.TenantID)+"?confirm=business-block", nil, "")
			require.Equal(t, 409, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "租户仍有业务数据")
			require.Equal(t, before, tenantPurgeFingerprint(t, f, result.TenantID))
			assertNoTenantPurgeAudit(t, f)
		})
	}
}

func TestIdentityTenantPurgeIntegrationGraceSettings(t *testing.T) {
	// BE-IDN-011 §4.11: HTTP schema, defaults, integer boundaries and live persistence.
	f := newSystemFixture(t, nil)
	w, out := systemRequest(t, f, "GET", "/admin/settings/tenant", nil, "")
	require.Equal(t, 200, w.Code)
	require.EqualValues(t, 7, systemGroupValue(t, out.Data, "purge_grace_days"))
	for _, days := range []int{0, 365, 7} {
		w, out = systemRequest(t, f, "PUT", "/admin/settings/tenant", map[string]any{"purge_grace_days": days}, "")
		require.Equal(t, 200, w.Code)
		require.EqualValues(t, days, systemGroupValue(t, out.Data, "purge_grace_days"))
	}
	for _, invalid := range []any{-1, 366, 0.5, "0", nil, true} {
		w, out = systemRequest(t, f, "PUT", "/admin/settings/tenant", map[string]any{"purge_grace_days": invalid}, "")
		require.Equal(t, 400, w.Code)
		require.Equal(t, "purge_grace_days", out.Details["key"])
	}
	w, out = systemRequest(t, f, "GET", "/admin/settings/tenant", nil, "")
	require.Equal(t, 200, w.Code)
	require.EqualValues(t, 7, systemGroupValue(t, out.Data, "purge_grace_days"))
	t.Log("HTTP grace default=7; 0/365/7 persisted; -1/366/fraction/string/null/bool=400 without changing saved value")
}

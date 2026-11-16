package bootstrap

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantPurgeIntegrationCompleteAndAudited(t *testing.T) {
	// BE-IDN-011 §4.5,7,9,11: full rows, no collateral changes, exact numeric audit.
	f := newTenantFixture(t)
	result := seedTenantPurge(t, f, "purge-b")
	peer := seedTenantPurge(t, f, "purge-peer")
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO channel_provider(channel,kind,name,config) VALUES('email','smtp','purge-global','{}')`))
	purgeGrace(t, f, 0)
	archiveTenantForPurge(t, f, result.TenantID)
	before := tenantPurgeCounts(t, f, result.TenantID)
	for table, count := range before {
		require.Positive(t, count, table+" must have real fixture rows")
	}
	platformBefore := tenantPurgeFingerprint(t, f, 1)
	peerBefore := tenantPurgeFingerprint(t, f, peer.TenantID)
	globalBefore := tenantPurgeFingerprint(t, f, 0)
	w, _ := f.request(t, "DELETE", tenantPath(result.TenantID)+"?confirm=purge-b", nil, "")
	require.Equal(t, 204, w.Code, w.Body.String())
	require.Empty(t, w.Body.String())
	after := tenantPurgeCounts(t, f, result.TenantID)
	for table, count := range after {
		require.Zero(t, count, table)
	}
	require.Equal(t, platformBefore, tenantPurgeFingerprint(t, f, 1))
	require.Equal(t, peerBefore, tenantPurgeFingerprint(t, f, peer.TenantID))
	require.Equal(t, globalBefore, tenantPurgeFingerprint(t, f, 0))
	assertTenantPurgeAudit(t, f, result.TenantID, before)
	w, _ = f.request(t, "GET", tenantPath(result.TenantID), nil, "")
	require.Equal(t, 404, w.Code)
	logTenantPurgeCounts(t, "deleted_counts", after)
	t.Log("confirm exact=204; all table counts=0 including soft-deleted users; platform/fully populated peer/global row fingerprints unchanged; grace=0 permits immediate purge")
}

func assertTenantPurgeAudit(t *testing.T, f *tenantFixture, id int64, counts map[string]int64) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []struct {
		Action, ResourceID, Result string
		After                      json.RawMessage
	}
	require.NoError(t, q.Table("audit_log").Select(`action,resource_id,result,"after"`).
		Where("action='system.tenant.purge' AND resource_id=? AND tenant_id=1", strconv.FormatInt(id, 10)).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "success", rows[0].Result)
	var after struct {
		Code string           `json:"code"`
		Rows map[string]int64 `json:"rows"`
	}
	require.NoError(t, json.Unmarshal(rows[0].After, &after))
	require.Equal(t, "purge-b", after.Code)
	require.Equal(t, counts, after.Rows)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rows[0].After, &fields))
	require.Len(t, fields, 2)
	t.Logf("audit action=%s resource_id=%s result=%s after=%s", rows[0].Action, rows[0].ResourceID, rows[0].Result, rows[0].After)
}

func TestIdentityTenantPurgeIntegrationPreconditionsAndReplay(t *testing.T) {
	// BE-IDN-011 §4.4–6: protect archive age, exact confirmation and idempotency.
	f := newTenantFixture(t)
	result := f.provision(t, f.template(t, "purge-preconditions").ID, "purge-eligible")
	path := tenantPath(result.TenantID) + "?confirm=purge-eligible"
	w, _ := f.request(t, "DELETE", path, nil, "")
	require.Equal(t, 409, w.Code)
	archiveTenantForPurge(t, f, result.TenantID)
	w, _ = f.request(t, "DELETE", path, nil, "")
	require.Equal(t, 409, w.Code)
	require.Contains(t, w.Body.String(), "归档未满 7 天")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	// A target tenant cannot override the platform's deletion grace period.
	require.NoError(t, q.Exec(`UPDATE tenant SET settings='{"purge_grace_days":0}' WHERE id=?`, result.TenantID).Error)
	f.service.UseSettings(&systemSettingStore{database: f.database})
	w, _ = f.request(t, "DELETE", path, nil, "")
	require.Equal(t, 409, w.Code)
	require.NoError(t, q.Exec("UPDATE tenant SET deleted_at=? WHERE id=?", f.now.Add(-7*24*time.Hour), result.TenantID).Error)
	for _, confirm := range []string{"", "PURGE-ELIGIBLE", "purge-eligible%20", "purge-eligibl", "x%27%20OR%201=1--"} {
		w, _ = f.request(t, "DELETE", tenantPath(result.TenantID)+"?confirm="+confirm, nil, "purge-replay")
		require.Equal(t, 400, w.Code)
	}
	for i := 0; i < 2; i++ {
		w, _ = f.request(t, "DELETE", path, nil, "purge-replay")
		require.Equal(t, 204, w.Code, w.Body.String())
	}
	w, out := f.request(t, "DELETE", tenantPath(result.TenantID)+"?confirm=changed", nil, "purge-replay")
	require.Equal(t, 409, w.Code)
	require.Equal(t, 10006, out.Code)
	w, _ = f.request(t, "DELETE", "/admin/tenants/1?confirm=default", nil, "")
	require.Equal(t, 409, w.Code)
	t.Log("unarchived409; default grace7=409; target override ignored; exact 7-day boundary=204; wrong/missing/case/space/injection confirms400; same replay204 changed confirm409/10006; platform409")
}

func TestIdentityTenantPurgeIntegrationPermissions(t *testing.T) {
	// BE-IDN-011 §4.12: even ordinary tenant super administrators cannot discover targets.
	f := newTenantFixture(t)
	result := f.provision(t, f.template(t, "purge-permissions").ID, "purge-own")
	tokens, _ := f.accept(t, result, "purge-own-admin")
	f.bearer = tokens.AccessToken
	for _, id := range []int64{1, result.TenantID, 999999} {
		for _, action := range []string{"archive", "restore", ""} {
			method, path := "POST", tenantPath(id)+"/"+action
			if action == "" {
				method, path = "DELETE", tenantPath(id)+"?confirm=purge-own"
			}
			w, _ := f.request(t, method, path, nil, "")
			require.Equal(t, 404, w.Code)
		}
	}
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	f.bearer = f.login(t).AccessToken
	w, _ := f.request(t, "DELETE", tenantPath(result.TenantID)+"?confirm=purge-own", nil, "")
	require.Equal(t, 404, w.Code)
	t.Log("ordinary tenant super (own/platform/missing targets) and platform operator: archive/restore/purge=404")
}

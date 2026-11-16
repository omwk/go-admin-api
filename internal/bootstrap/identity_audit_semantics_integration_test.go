package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func latestIdentityAudit(t *testing.T, f *tenantFixture, action string) system.AuditRecord {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row system.AuditRecord
	require.NoError(t, q.Table("audit_log").Select(`id,actor_name,actor_tenant_name,resource_label,action,resource_id,"before","after",host(ip) AS ip,user_agent,error_code,error_message,result`).
		Where("tenant_id=1 AND action=?", action).Order("id DESC").Take(&row).Error)
	return row
}

func TestIdentityAuditSemanticsIntegrationReadPerRequest(t *testing.T) {
	// BE-IDN-009 §4.1–2/7/8: twelve foreign rows produce exactly one access event.
	f := newTenantFixture(t)
	for i := range 12 {
		require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(code,name) VALUES(?,?)", fmt.Sprintf("audit-%d", i), fmt.Sprintf("Audit %d", i)))
	}
	before := f.count(t, "audit_log", 1)
	w, _ := f.request(t, "GET", "/admin/tenants", nil, "")
	require.Equal(t, 200, w.Code)
	after := f.count(t, "audit_log", 1)
	require.EqualValues(t, 1, after-before)
	row := latestIdentityAudit(t, f, "system.tenant.list")
	require.Equal(t, "0", row.ResourceID)
	require.JSONEq(t, "null", string(row.Before))
	var scope struct {
		Tenants []int64 `json:"accessed_tenants"`
		Count   int     `json:"count"`
	}
	require.NoError(t, json.Unmarshal(row.After, &scope))
	require.Len(t, scope.Tenants, 12)
	require.Equal(t, 12, scope.Count)
	require.NotContains(t, scope.Tenants, int64(1))
	require.Equal(t, "192.0.2.9", row.IP)
	require.Equal(t, "audit-semantics-integration", row.UserAgent)
	t.Logf("GET /admin/tenants: total before=%d after=%d delta=%d; accessed_tenants=12 count=12 before=null", before, after, after-before)
}

func TestIdentityAuditSemanticsIntegrationOwnTenantNoAudit(t *testing.T) {
	// BE-IDN-009 §4.3: ordinary users see only their tenant and create no read audit.
	f := newTenantFixture(t)
	template := f.template(t, "own-audit")
	result := f.provision(t, template.ID, "own-audit")
	tokens, _ := f.accept(t, result, "own-audit-admin")
	f.bearer = tokens.AccessToken
	before := f.count(t, "audit_log", result.TenantID)
	w, _ := f.request(t, "GET", "/admin/tenants", nil, "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, before, f.count(t, "audit_log", result.TenantID))
	t.Log("ordinary own-tenant GET /admin/tenants: audit delta=0")
}

func TestIdentityAuditSemanticsIntegrationChangedFieldsAndNoop(t *testing.T) {
	// BE-IDN-009 §4.4–5: no contextual fields or unchanged values in a write diff.
	f := newTenantFixture(t)
	before := f.count(t, "audit_log", 1)
	w, _ := f.request(t, "PUT", tenantPath(1), map[string]any{"name": "Renamed audit tenant"}, "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, before+1, f.count(t, "audit_log", 1))
	row := latestIdentityAudit(t, f, "system.tenant.rename")
	require.Equal(t, "192.0.2.9", row.IP)
	require.Equal(t, "audit-semantics-integration", row.UserAgent)
	var old map[string]any
	require.NoError(t, json.Unmarshal(row.Before, &old))
	require.Len(t, old, 1)
	require.Contains(t, old, "name")
	require.JSONEq(t, `{"name":"Renamed audit tenant"}`, string(row.After))
	w, _ = f.request(t, "PUT", tenantPath(1), map[string]any{"name": "Renamed audit tenant"}, "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, before+1, f.count(t, "audit_log", 1))
	t.Log("name-only diff contains exactly name; write audit has IP/UA; identical PUT audit delta=0")
}

func TestIdentityAuditSemanticsIntegrationImmutableNamesAndDeletion(t *testing.T) {
	// BE-IDN-009 §4.6/12–14: actor, tenant and resource names are persisted evidence.
	f := newTenantFixture(t)
	template := f.template(t, "original-label")
	created := latestIdentityAudit(t, f, "system.tenant.template.create")
	require.Equal(t, f.actor.User.Username, *created.ActorName)
	require.Equal(t, "default", *created.ActorTenantName)
	require.Equal(t, "original-label", *created.ResourceLabel)
	require.JSONEq(t, "null", string(created.Before))
	require.NotEqual(t, "null", string(created.After))
	w, _ := f.request(t, "PUT", templatePath(template.ID), map[string]any{"name": "renamed-label"}, "")
	require.Equal(t, 200, w.Code)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET username=? WHERE id=?", "renamed-actor", f.actor.User.ID))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE tenant SET code=? WHERE id=1", "renamed-tenant"))
	w, _ = f.request(t, "DELETE", templatePath(template.ID), nil, "")
	require.Equal(t, 204, w.Code)
	deleted := latestIdentityAudit(t, f, "system.tenant.template.delete")
	require.Equal(t, "renamed-label", *deleted.ResourceLabel)
	require.NotEqual(t, "null", string(deleted.Before))
	require.JSONEq(t, "null", string(deleted.After))
	store := &systemAuditStore{database: f.database}
	rows, total, err := store.AuditRecords(f.ctx, 1, system.AuditFilter{Action: created.Action, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, created.ActorName, rows[0].ActorName)
	require.Equal(t, created.ActorTenantName, rows[0].ActorTenantName)
	require.Equal(t, created.ResourceLabel, rows[0].ResourceLabel)
	t.Logf("after actor/tenant/resource rename and physical delete: actor_name=%s actor_tenant_name=%s resource_label=%s; delete label=%s", *rows[0].ActorName, *rows[0].ActorTenantName, *rows[0].ResourceLabel, *deleted.ResourceLabel)
}

func TestIdentityAuditSemanticsIntegrationFailureReason(t *testing.T) {
	// BE-IDN-009 §4.15: a failed login stores a code and a stable public message.
	f := newTenantFixture(t)
	w, _ := f.request(t, "POST", "/auth/login", map[string]any{"tenant_id": 1, "account": f.user.Username, "password": accountPassword(t)}, "")
	require.Equal(t, 401, w.Code)
	row := latestIdentityAudit(t, f, "identity.login")
	require.Equal(t, "failure", row.Result)
	require.NotNil(t, row.ErrorCode)
	require.Equal(t, 20101, *row.ErrorCode)
	require.NotNil(t, row.ErrorMessage)
	require.Equal(t, "用户名或密码错误", *row.ErrorMessage)
	t.Logf("failed login error_code=%d error_message=%s; no supplied password, token or database cause persisted", *row.ErrorCode, *row.ErrorMessage)
}

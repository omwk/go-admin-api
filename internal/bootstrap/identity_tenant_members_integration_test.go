package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantRecoveryIntegrationMembersAndCounts(t *testing.T) {
	// BE-IDN-002 §3.6–7: exact public fields, correct counts and non-deleted members only.
	f := newTenantFixture(t)
	template := f.template(t, "members")
	target := f.provision(t, template.ID, "members-b")
	f.provision(t, template.ID, "members-foreign")
	admin := recoveryAdmin(t, f, target.TenantID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, row := range []model.User{
		{TenantID: target.TenantID, Username: "active-member", DepartmentID: admin.DepartmentID, Status: "active"},
		{TenantID: target.TenantID, Username: "active-admin", DepartmentID: admin.DepartmentID, Status: "active", IsSuper: true},
		{TenantID: target.TenantID, Username: "locked-admin", DepartmentID: admin.DepartmentID, Status: "locked", IsSuper: true},
		{TenantID: target.TenantID, Username: "deleted-admin", DepartmentID: admin.DepartmentID, Status: "active", IsSuper: true},
	} {
		require.NoError(t, q.Create(&row).Error)
		if row.Username == "deleted-admin" {
			require.NoError(t, q.Delete(&row).Error)
		}
	}
	w, out := f.request(t, "GET", tenantPath(target.TenantID)+"/members", nil, "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var page identity.AdminPage[map[string]any]
	require.NoError(t, json.Unmarshal(out.Data, &page))
	require.EqualValues(t, 4, page.Total)
	active := int64(0)
	for _, member := range page.List {
		require.Len(t, member, 6)
		for _, key := range []string{"id", "account", "nickname", "status", "is_super", "created_at"} {
			require.Contains(t, member, key)
		}
		require.NotEqual(t, "deleted-admin", member["account"])
		require.NotEqual(t, "members-foreign-admin", member["account"])
		if member["status"] == "active" && member["is_super"] == true {
			active++
		}
	}
	w, out = f.request(t, "GET", "/admin/tenants", nil, "")
	require.Equal(t, 200, w.Code)
	var tenants identity.AdminPage[model.TenantSummary]
	require.NoError(t, json.Unmarshal(out.Data, &tenants))
	found := false
	for _, tenant := range tenants.List {
		if tenant.ID == target.TenantID {
			found = true
			require.Equal(t, page.Total, tenant.MemberCount)
			require.Equal(t, active, tenant.AdminActiveCount)
			require.EqualValues(t, 1, tenant.AdminActiveCount)
		}
	}
	require.True(t, found)
	w, out = f.request(t, "GET", tenantPath(target.TenantID)+"/members?page_size=1&page=2", nil, "")
	require.Equal(t, 200, w.Code)
	var second identity.AdminPage[model.TenantMember]
	require.NoError(t, json.Unmarshal(out.Data, &second))
	require.Len(t, second.List, 1)
	require.EqualValues(t, 4, second.Total)
	require.Equal(t, 2, second.Page)
	t.Log("§3.6 members=4, exact six safe fields, no foreign/deleted users; paging works; §3.7 member_count=4/admin_active_count=1 matches members, locked/disabled/deleted excluded from active admins")
}

func TestIdentityTenantRecoveryIntegrationPlatformOnly(t *testing.T) {
	// BE-IDN-002 §3.6,9: own, existing foreign and missing targets share the platform boundary.
	f := newTenantFixture(t)
	template := f.template(t, "member-boundary")
	target := f.provision(t, template.ID, "member-boundary-b")
	tokens, _ := f.accept(t, target, "member-boundary-b-admin")
	w, _ := f.request(t, "GET", tenantPath(999999)+"/members", nil, "")
	require.Equal(t, 404, w.Code)
	w, _ = f.request(t, "POST", tenantPath(999999)+"/admin-invite", nil, "")
	require.Equal(t, 404, w.Code)
	before := tenantAllCounts(t, f)
	f.bearer = tokens.AccessToken
	for _, id := range []int64{1, target.TenantID, 999999} {
		for _, attempt := range []struct{ method, suffix string }{
			{"GET", "/members"}, {"POST", "/admin-invite"}, {"POST", "/admin-invite?force=true"},
		} {
			w, out := f.request(t, attempt.method, tenantPath(id)+attempt.suffix, nil, "")
			require.Equal(t, 404, w.Code)
			require.Equal(t, 10003, out.Code)
		}
	}
	require.Equal(t, before, tenantAllCounts(t, f))
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	f.bearer = f.login(t).AccessToken
	for _, attempt := range []struct{ method, suffix string }{{"GET", "/members"}, {"POST", "/admin-invite"}} {
		w, _ := f.request(t, attempt.method, tenantPath(target.TenantID)+attempt.suffix, nil, "")
		require.Equal(t, 403, w.Code)
	}
	t.Log("§3.6 ordinary tenant administrator: own/foreign/missing targets=404; no side effects; missing permission=403")
}

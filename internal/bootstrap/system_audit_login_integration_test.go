package bootstrap

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestSystemAuditIntegrationLoginHistory(t *testing.T) {
	// S4 §5.7: six existing-table rows, with complete stored metadata.
	f := newSystemFixture(t, nil)
	user := strconv.FormatInt(f.user.ID, 10)
	for i := range 7 {
		action, result := "identity.login", "success"
		if i >= 3 {
			result = "failure"
		}
		if i == 6 {
			action = "identity.other"
		}
		insertS4Audit(t, f, 1, system.AuditRecord{ActorType: "user", ActorID: &f.user.ID, Action: action,
			ResourceType: "user", ResourceID: user, Result: result, IP: "192.0.2.20", UserAgent: "S4 integration client",
			CreatedAt: f.now.Add(time.Duration(i) * time.Second)})
	}
	window := s4AuditWindow(f.now, f.now.Add(6*time.Second))
	response, envelope := systemRequest(t, f, "GET", "/admin/login-logs?"+window+"&action=identity.other", nil, "")
	require.Equal(t, 200, response.Code)
	var page system.Page[system.LoginLog]
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 6, page.Total)
	require.Len(t, page.List, 6)
	for i, row := range page.List {
		require.Equal(t, f.now.Add(time.Duration(5-i)*time.Second), row.CreatedAt)
		require.Equal(t, &f.user.ID, row.UserID)
		require.Equal(t, f.user.Username, row.Username)
		require.Equal(t, "192.0.2.20", row.IP)
		require.Equal(t, "S4 integration client", row.UserAgent)
	}
	for _, result := range []string{"success", "failure"} {
		response, envelope = systemRequest(t, f, "GET", "/admin/login-logs?"+window+"&result="+result+"&user_id="+user, nil, "")
		require.Equal(t, 200, response.Code)
		require.NoError(t, json.Unmarshal(envelope.Data, &page))
		require.EqualValues(t, 3, page.Total)
	}
	response, envelope = systemRequest(t, f, "GET", "/admin/login-logs?"+window+"&user_id=999999", nil, "")
	require.Equal(t, 200, response.Code)
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.Empty(t, page.List)
	for _, query := range []string{"user_id=0", "user_id=-1", "user_id=bad", "page_size=101", "result=invalid", "from=invalid"} {
		response, _ = systemRequest(t, f, "GET", "/admin/login-logs?"+query, nil, "")
		require.Equal(t, 400, response.Code)
	}
	t.Log("S4 #7: audit_log success=3 failure=3, ordered timestamps, IP/UA retained; source action fixed")
}

func TestSystemAuditIntegrationUnknownLoginAndWriterCompatibility(t *testing.T) {
	// S4 §5.8/§3.2: use the real, unchanged login writer, even where it omits IP/UA.
	f := newSystemFixture(t, nil)
	unknown, err := refresh.Generate()
	require.NoError(t, err)
	password, err := refresh.Generate()
	require.NoError(t, err)
	_, err = f.identityFixture.service.Login(f.ctx, 1, unknown, password, identity.RequestMeta{IP: "192.0.2.21", UserAgent: "S4 actual login"})
	require.Error(t, err)
	response, envelope := systemRequest(t, f, "GET", "/admin/login-logs?result=failure", nil, "")
	require.Equal(t, 200, response.Code)
	require.NotContains(t, response.Body.String(), unknown)
	require.NotContains(t, response.Body.String(), password)
	var page system.Page[system.LoginLog]
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.Len(t, page.List, 1)
	require.Nil(t, page.List[0].UserID)
	require.Equal(t, "unknown", page.List[0].ResourceID)
	require.Equal(t, "未知账号", page.List[0].Username)
	require.Empty(t, page.List[0].IP)
	require.Empty(t, page.List[0].UserAgent)
	t.Log("S4 #8: actual unknown login -> 未知账号 / resource_id=unknown / user_id=null; original writer omits IP/UA")
}

func TestSystemAuditIntegrationPermissionsAndMenus(t *testing.T) {
	// S4 §5.12/14: real auth menus and fresh permission checks.
	f := newSystemFixture(t, nil)
	response, menus := profileRequest(t, f.router, "/api/v1/auth/menus", f.bearer)
	require.Equal(t, 200, response.Code)
	found := map[string]identity.MenuNode{}
	for _, root := range menus.Menus {
		if root.Path == nil || *root.Path != "/system" {
			continue
		}
		for _, child := range root.Children {
			if child.Path != nil {
				found[*child.Path] = child
			}
		}
	}
	for _, path := range []string{"/system/audit-logs", "/system/login-logs"} {
		require.Contains(t, found, path)
		require.Equal(t, "system:audit:list", *found[path].PermissionCode)
		require.Greater(t, found[path].Sort, found["/system/depts"].Sort)
	}
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET is_super=false WHERE tenant_id=1 AND id=?", f.actor.User.ID))
	require.NoError(t, f.database.Exec(f.ctx, "DELETE FROM sys_user_role WHERE tenant_id=1 AND user_id=?", f.actor.User.ID))
	for _, path := range []string{"/admin/audit-logs", "/admin/login-logs"} {
		response, envelope := systemRequest(t, f, "GET", path, nil, "")
		require.Equal(t, 403, response.Code)
		require.Equal(t, 20107, envelope.Code)
	}
	response, envelope := adminRequest(t, f.adminFixture, "POST", resourcePath("users", f.user.ID)+"/unlock", nil, "")
	require.Equal(t, 403, response.Code)
	require.Equal(t, 20107, envelope.Code)
	f.bearer = ""
	for _, path := range []string{"/admin/audit-logs", "/admin/login-logs"} {
		response, _ := systemRequest(t, f, "GET", path, nil, "")
		require.Equal(t, 401, response.Code)
	}
	t.Log("S4 #12/#14: both auth/menus pages present; missing audit permission and missing user:update each return 403")
}

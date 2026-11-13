package bootstrap

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func s3aValidateHTTP(t *testing.T, method, path string, response *httptest.ResponseRecorder) {
	t.Helper()
	parts := strings.Split(strings.Split(path, "?")[0], "/")
	for i := 2; i < len(parts); i++ {
		if _, err := strconv.ParseInt(parts[i], 10, 64); err == nil {
			parts[i] = "{id}"
		} else if parts[i-1] == "templates" {
			parts[i] = "{code}"
		}
	}
	validateOpenAPIResponse(t, readSystemOpenAPI(t), "/api/v1/admin"+strings.Join(parts, "/"), strings.ToLower(method), response.Code, response.Body.Bytes())
}

func s3aNotificationRoutes() map[string]string {
	return map[string]string{
		"GET /api/v1/admin/notifications":                        permission.SystemNotifyList,
		"GET /api/v1/admin/notifications/unread-count":           permission.SystemNotifyList,
		"POST /api/v1/admin/notifications/read-all":              permission.SystemNotifyRead,
		"PUT /api/v1/admin/notifications/{id}/read":              permission.SystemNotifyRead,
		"GET /api/v1/admin/notifications/preferences":            "",
		"PUT /api/v1/admin/notifications/preferences":            "",
		"GET /api/v1/admin/notification-settings":                permission.SystemNotifyManage,
		"PUT /api/v1/admin/notification-settings":                permission.SystemNotifyManage,
		"GET /api/v1/admin/notifications/providers":              permission.SystemNotifyManage,
		"POST /api/v1/admin/notifications/providers":             permission.SystemNotifyManage,
		"PUT /api/v1/admin/notifications/providers/{id}":         permission.SystemNotifyManage,
		"DELETE /api/v1/admin/notifications/providers/{id}":      permission.SystemNotifyManage,
		"POST /api/v1/admin/notifications/providers/{id}/test":   permission.SystemNotifyManage,
		"GET /api/v1/admin/notifications/templates":              permission.SystemNotifyManage,
		"PUT /api/v1/admin/notifications/templates/{code}":       permission.SystemNotifyManage,
		"GET /api/v1/admin/notifications/deliveries":             permission.SystemNotifyList,
		"POST /api/v1/admin/notifications/deliveries/{id}/retry": permission.SystemNotifyManage,
	}
}

func TestSystemNotificationPermissionsIntegrationS3a(t *testing.T) {
	// S3a §4.8: setting:write does not imply notify:manage or notify:list.
	f := newS3aFixture(t)
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO sys_role(tenant_id,code,name,data_scope,status) VALUES(1,'s3a-notify','s3a-notify','self','active')`))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO sys_user_role(tenant_id,user_id,role_id) SELECT 1,?,id FROM sys_role WHERE tenant_id=1 AND code='s3a-notify'`, f.user.ID))
	s3aGrant(t, f, permission.SystemSettingWrite)
	f.bearer = f.login(t).AccessToken
	for route, code := range s3aNotificationRoutes() {
		if code != permission.SystemNotifyManage {
			continue
		}
		parts := strings.SplitN(route, " ", 2)
		path := strings.TrimPrefix(strings.NewReplacer("{id}", "1", "{code}", "notification.default").Replace(parts[1]), "/api/v1/admin")
		w := s3aRequest(t, f, parts[0], path, "", nil)
		require.Equal(t, 403, w.Code, route+": "+w.Body.String())
	}
	for _, path := range []string{"/notifications", "/notifications/unread-count", "/notifications/deliveries"} {
		require.Equal(t, 403, s3aRequest(t, f, http.MethodGet, path, "", nil).Code)
	}
	require.Equal(t, 200, s3aRequest(t, f, http.MethodPost, "/notifications/read-all", "", nil).Code)
	require.Equal(t, 200, s3aRequest(t, f, http.MethodGet, "/notifications/preferences", "", nil).Code)
	s3aGrant(t, f, permission.SystemNotifyManage)
	for _, path := range []string{"/notifications/providers", "/notifications/templates", "/notification-settings"} {
		require.Equal(t, 200, s3aRequest(t, f, http.MethodGet, path, "", nil).Code)
	}
	require.Equal(t, 403, s3aRequest(t, f, http.MethodGet, "/notifications/deliveries", "", nil).Code)
	s3aGrant(t, f, permission.SystemNotifyList)
	require.Equal(t, 200, s3aRequest(t, f, http.MethodGet, "/notifications/deliveries", "", nil).Code)
	require.Equal(t, 200, s3aRequest(t, f, http.MethodGet, "/notifications", "", nil).Code)
	f.bearer = ""
	for _, path := range []string{"/notifications/preferences", "/notifications/unread-count", "/notifications/providers"} {
		require.Equal(t, 401, s3aRequest(t, f, http.MethodGet, path, "", nil).Code)
	}
	t.Log("setting:write alone: all manage operations=403; notify:manage=200; deliveries require notify:list; own read/preferences need login only")
}

func s3aGrant(t *testing.T, f *s3aFixture, code string) {
	t.Helper()
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO sys_role_permission(tenant_id,role_id,permission_id)
 SELECT 1,r.id,p.id FROM sys_role r CROSS JOIN sys_permission p WHERE r.tenant_id=1 AND r.code='s3a-notify' AND p.tenant_id=1 AND p.code=?`, code))
}

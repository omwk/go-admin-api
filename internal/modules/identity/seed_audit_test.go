package identity

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestAuditPermissionAndMenusAppendAfterS3a(t *testing.T) {
	// S4 §3.1/§5.14: additions preserve published S3a array positions.
	codes := permission.All()
	require.Equal(t, permission.SystemAuditList, codes[115].Code)
	menus := seedMenus()
	require.Equal(t, "/system/audit-logs", *menus[17].Row.Path)
	require.Equal(t, "/system/login-logs", *menus[18].Row.Path)
	for _, menu := range menus[17:19] {
		require.Equal(t, "/system", menu.ParentPath)
		require.Equal(t, permission.SystemAuditList, *menu.Row.PermissionCode)
	}
}

func TestTenantPermissionsAndMenusAppendAfterS4(t *testing.T) {
	// S5 §4.13: preserve published positions and append tenant menu/button capabilities.
	codes := permission.All()
	require.Equal(t, permission.SystemTenantList, codes[116].Code)
	require.Equal(t, permission.SystemTenantManage, codes[117].Code)
	menus := seedMenus()
	require.Equal(t, "/system/tenants", *menus[19].Row.Path)
	require.Equal(t, "/system", menus[19].ParentPath)
	require.Equal(t, permission.SystemTenantList, *menus[19].Row.PermissionCode)
	require.Equal(t, "/system/tenants/manage", *menus[20].Row.Path)
	require.Equal(t, "/system/tenants", menus[20].ParentPath)
	require.Equal(t, "button", menus[20].Row.Type)
	require.Equal(t, permission.SystemTenantManage, *menus[20].Row.PermissionCode)
}

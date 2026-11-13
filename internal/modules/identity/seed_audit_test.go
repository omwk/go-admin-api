package identity

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestAuditPermissionAndMenusAppendAfterS3a(t *testing.T) {
	// S4 §3.1/§5.14: additions preserve published S3a array positions.
	codes := permission.All()
	require.Equal(t, permission.SystemAuditList, codes[len(codes)-1].Code)
	menus := seedMenus()
	require.Equal(t, "/system/audit-logs", *menus[len(menus)-2].Row.Path)
	require.Equal(t, "/system/login-logs", *menus[len(menus)-1].Row.Path)
	for _, menu := range menus[len(menus)-2:] {
		require.Equal(t, "/system", menu.ParentPath)
		require.Equal(t, permission.SystemAuditList, *menu.Row.PermissionCode)
	}
}

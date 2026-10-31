package scope

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccessRejectsMissingIdentityAndRoles(t *testing.T) {
	// REQ-IAM-008: neither is_super nor an absent policy grants data access.
	for _, access := range []Access{{}, {Tenant: 1, User: 2}, {Tenant: 1, User: 2, Roles: []Role{{Kind: "unknown"}}}} {
		_, err := From(WithAccess(context.Background(), access))
		require.Error(t, err)
	}
}

func TestDeclarationsAreFixedAndUnknownEntityIsDenied(t *testing.T) {
	for _, entity := range []string{"sys_user", "sys_refresh_token", "sys_role", "sys_menu", "sys_permission"} {
		declaration, err := Lookup(entity)
		require.NoError(t, err)
		require.Equal(t, entity, declaration.Alias)
		require.Equal(t, "tenant_id", declaration.TenantColumn)
		require.NotEmpty(t, declaration.DepartmentSource)
	}
	_, err := Lookup("unregistered")
	require.Error(t, err)
}

func TestQueryDeclarationsRejectUnknownMethodsAndRoots(t *testing.T) {
	_, err := Query("Admin.Unregistered")
	require.Error(t, err)
	require.False(t, Allows("Admin.User", "sys_role"))
	require.False(t, Allows("Admin.User", "unknown"))
	for _, port := range []string{"Admin", "Sessions", "Auth", "Profile", "Seed"} {
		for _, method := range QueryNames(port) {
			declaration, err := Query(port + "." + method)
			require.NoError(t, err)
			require.NotEmpty(t, declaration.Policy)
			for _, root := range declaration.Roots {
				require.NotEmpty(t, root.Entity)
				require.NotEmpty(t, root.Alias)
				require.NotEmpty(t, root.TenantColumn)
				require.NotEmpty(t, root.DepartmentSource)
			}
		}
	}
}

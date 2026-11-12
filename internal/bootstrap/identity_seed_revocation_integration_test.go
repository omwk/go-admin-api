package bootstrap

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestSeedIntegrationDoesNotResurrectRevokedGrants(t *testing.T) {
	f, _ := seededProfileFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	// S1b: only super_admin gains missing grants; all other revocations remain final.
	for _, code := range []string{identity.OperatorRoleCode, identity.SalesRoleCode} {
		var role model.Role
		require.NoError(t, q.Select("id").Where("tenant_id = ? AND code = ?", 1, code).Take(&role).Error)
		for _, row := range []any{&model.RolePermission{}, &model.RoleMenu{}} {
			require.NoError(t, q.Where("tenant_id = ? AND role_id = ?", 1, role.ID).Delete(row).Error)
		}
	}
	require.NoError(t, q.Where("tenant_id = ?", 1).Delete(&model.UserRole{}).Error)
	counts, hash := identityRowCounts(t, f), seededAdminHash(t, f)
	require.NoError(t, SeedIdentity(f.ctx, f.database, f.password))
	require.Equal(t, counts, identityRowCounts(t, f))
	require.Equal(t, hash, seededAdminHash(t, f))
}

func TestSeedIntegrationExistingRoleDoesNotReceiveDefaultGrants(t *testing.T) {
	f := newIdentityFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	role := model.Role{TenantID: 1, Code: identity.OperatorRoleCode, Name: "existing", DataScope: "dept", Status: "disabled"}
	require.NoError(t, q.Create(&role).Error)
	require.NoError(t, SeedIdentity(f.ctx, f.database, f.password))
	for _, table := range []string{"sys_role_permission", "sys_role_menu"} {
		var count int64
		require.NoError(t, q.Table(table).Where("tenant_id = ? AND role_id = ?", 1, role.ID).Count(&count).Error)
		require.Zero(t, count)
	}
	var preserved model.Role
	require.NoError(t, q.Select(adminRoleColumns).Where("id = ?", role.ID).Take(&preserved).Error)
	require.Equal(t, role, preserved)
}

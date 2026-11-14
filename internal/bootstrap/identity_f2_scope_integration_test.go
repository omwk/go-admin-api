package bootstrap

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestAdminF2IntegrationUserScopeAndMissingAssociations(t *testing.T) {
	f := newAdminFixture(t)
	for _, path := range []string{"/depts/9999999", "/roles/9999999", "/roles/9999999/menus", "/roles/9999999/permissions", "/roles/9999999/depts", "/menus/9999999", "/users/9999999/roles"} {
		f2AdminGet(t, f, path, 404)
	}
	for _, path := range []string{"/depts/0", "/users/invalid", "/users/0/roles", "/roles/0", "/roles/invalid/menus", "/roles/0/permissions", "/roles/0/depts", "/menus/0"} {
		f2AdminGet(t, f, path, 400)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	ordinaryScopeActor(t, f.identityFixture, f.actor.User.ID)
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id=1 AND code=?", identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	for _, suffix := range []string{"", "/roles"} {
		f2AdminGet(t, f, resourcePath("users", f.user.ID)+suffix, 404)
	}
	f2AdminGet(t, f, resourcePath("users", f.actor.User.ID), 200)
}

package bootstrap

import (
	"net/http"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestIdentitySuperScopeIntegrationInvitedFirstUserRoleAndMenus(t *testing.T) {
	// BE-IDN-006 §4 #3/#5/#8/#9: first-user election adds one built-in role in the invitation transaction.
	for _, preset := range []bool{false, true} {
		t.Run(map[bool]string{false: "no_preset", true: "already_preset"}[preset], func(t *testing.T) {
			f := newTenantFixture(t)
			target := f.provision(t, f.template(t, "super-first").ID, "super-first")
			admin := recoveryAdmin(t, f, target.TenantID)
			before := superScopeRoleCodes(t, f.identityFixture, target.TenantID, admin.ID)
			roles := []int64{}
			if preset {
				q, cancel := f.database.WithContext(f.ctx)
				defer cancel()
				var role model.Role
				require.NoError(t, q.Select("id").Where("tenant_id=? AND code=?", target.TenantID, identity.SuperAdminRoleCode).Take(&role).Error)
				roles = append(roles, role.ID)
			}
			accounts := recoveryAccounts(f)
			password := accountPassword(t)
			input := identity.AcceptInviteInput{Token: firstUserInvite(t, f, admin, "super-first-user", roles), Password: password}
			user, err := accounts.AcceptInvite(f.ctx, target.TenantID, input, identity.RequestMeta{})
			require.NoError(t, err)
			require.Equal(t, []string{identity.SuperAdminRoleCode}, superScopeRoleCodes(t, f.identityFixture, target.TenantID, user.ID))
			_, err = accounts.AcceptInvite(f.ctx, target.TenantID, input, identity.RequestMeta{})
			require.Error(t, err)
			require.Equal(t, []string{identity.SuperAdminRoleCode}, superScopeRoleCodes(t, f.identityFixture, target.TenantID, user.ID))
			tokens, err := f.identityFixture.service.Login(f.ctx, target.TenantID, user.Username, password, identity.RequestMeta{})
			require.NoError(t, err)
			w, menus := profileRequest(t, f.router, "/api/v1/auth/menus", tokens.AccessToken)
			require.Equal(t, http.StatusOK, w.Code)
			require.NotEmpty(t, menus.Menus)
			input = identity.AcceptInviteInput{Token: firstUserInvite(t, f, admin, "ordinary-next", nil), Password: accountPassword(t)}
			next, err := accounts.AcceptInvite(f.ctx, target.TenantID, input, identity.RequestMeta{})
			require.NoError(t, err)
			require.Empty(t, superScopeRoleCodes(t, f.identityFixture, target.TenantID, next.ID))
			require.Equal(t, before, superScopeRoleCodes(t, f.identityFixture, target.TenantID, admin.ID))
			t.Log("BE-IDN-006 #3/#5/#8/#9: first invitee has exactly one super_admin; menus=200/nonempty; replay adds no binding; next invitee and existing admin roles unchanged")
		})
	}
}

func TestIdentitySuperScopeIntegrationRegisteredFirstUserRole(t *testing.T) {
	// BE-IDN-006 §4 #4/#7/#9: registration elects a super once and leaves later users unchanged.
	f := newRegistrationFixture(t)
	f.settings(t, map[string]any{"allow_self_registration": true})
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Model(&model.User{}).Where("tenant_id=1").Update("is_super", false).Error)
	before := superScopeRoleCodes(t, f.identityFixture, 1, f.user.ID)
	first := registrationInput(t, "first-super-registration")
	result := f.register(t, first, http.StatusCreated)
	store := &registrationFailureStore{accountStore: &accountStore{f.store}, activate: true}
	f.accounts.Store = store
	f.verify(t, result.Link, http.StatusServiceUnavailable)
	user := registrationUser(t, f, 1, first.Account)
	require.False(t, user.IsSuper)
	require.Equal(t, "disabled", user.Status)
	require.Empty(t, superScopeRoleCodes(t, f.identityFixture, 1, user.ID))
	store.activate = false
	f.verify(t, result.Link, http.StatusNoContent)
	user = registrationUser(t, f, 1, first.Account)
	require.True(t, user.IsSuper)
	require.Equal(t, []string{identity.SuperAdminRoleCode}, superScopeRoleCodes(t, f.identityFixture, 1, user.ID))
	f.verify(t, result.Link, http.StatusBadRequest)
	next := registrationInput(t, "next-registration")
	f.verify(t, f.register(t, next, http.StatusCreated).Link, http.StatusNoContent)
	ordinary := registrationUser(t, f, 1, next.Account)
	require.False(t, ordinary.IsSuper)
	require.Empty(t, superScopeRoleCodes(t, f.identityFixture, 1, ordinary.ID))
	require.Equal(t, before, superScopeRoleCodes(t, f.identityFixture, 1, f.user.ID))
	t.Log("BE-IDN-006 #4/#7/#9: failed activation rolls back super flag/role; retry grants one super_admin; next registration stays ordinary; existing roles unchanged")
}

func TestIdentitySuperScopeIntegrationInvitationRoleRollback(t *testing.T) {
	f := newTenantFixture(t)
	target := f.provision(t, f.template(t, "super-rollback").ID, "super-rollback")
	admin := recoveryAdmin(t, f, target.TenantID)
	before := f.count(t, "sys_user_role", target.TenantID)
	accounts := recoveryAccounts(f)
	accounts.Store = &failingFirstUserStore{&accountStore{f.identityFixture.store}}
	input := identity.AcceptInviteInput{Token: firstUserInvite(t, f, admin, "rollback-super", nil), Password: accountPassword(t)}
	_, err := accounts.AcceptInvite(f.ctx, target.TenantID, input, identity.RequestMeta{})
	require.Error(t, err)
	require.EqualValues(t, 1, f.count(t, "sys_user", target.TenantID))
	require.Equal(t, before, f.count(t, "sys_user_role", target.TenantID))
	accounts.Store = &accountStore{f.identityFixture.store}
	user, err := accounts.AcceptInvite(f.ctx, target.TenantID, input, identity.RequestMeta{})
	require.NoError(t, err)
	require.Equal(t, []string{identity.SuperAdminRoleCode}, superScopeRoleCodes(t, f.identityFixture, target.TenantID, user.ID))
	t.Log("BE-IDN-006 transaction: invitation failure rolls back user and role binding; same invitation retries successfully")
}

func TestIdentitySuperScopeIntegrationExistingAdministratorUnchanged(t *testing.T) {
	// BE-IDN-006 §4 #9: accepting a bound recovery invitation does not backfill existing supers.
	f := newTenantFixture(t)
	target := f.provision(t, f.template(t, "existing-super").ID, "existing-super")
	admin := recoveryAdmin(t, f, target.TenantID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Where("tenant_id=? AND user_id=?", target.TenantID, admin.ID).Delete(&model.UserRole{}).Error)
	accounts := recoveryAccounts(f)
	input := identity.AcceptInviteInput{Token: inviteRaw(t, target.InviteURL), Password: accountPassword(t)}
	user, err := accounts.AcceptInvite(f.ctx, target.TenantID, input, identity.RequestMeta{})
	require.NoError(t, err)
	require.Equal(t, admin.ID, user.ID)
	require.Empty(t, superScopeRoleCodes(t, f.identityFixture, target.TenantID, admin.ID))
	t.Log("BE-IDN-006 #9: bound invitation activates existing super; its empty role collection remains empty")
}

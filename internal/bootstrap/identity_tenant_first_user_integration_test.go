package bootstrap

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantRecoveryIntegrationFirstUserAndPresetRoles(t *testing.T) {
	// BE-IDN-002 §3.8: the acceptance transaction elects the first active administrator.
	f := newTenantFixture(t)
	template := f.template(t, "first-user")
	target := f.provision(t, template.ID, "first-user-b")
	admin := recoveryAdmin(t, f, target.TenantID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	role := model.Role{TenantID: target.TenantID, Code: "invited-reader", Name: "reader", DataScope: "self", Status: "active"}
	require.NoError(t, q.Create(&role).Error)
	accounts := recoveryAccounts(f)
	for i, name := range []string{"first-registered", "second-registered"} {
		raw := firstUserInvite(t, f, admin, name, []int64{role.ID})
		user, err := accounts.AcceptInvite(f.ctx, target.TenantID, identity.AcceptInviteInput{Token: raw, Password: accountPassword(t)}, identity.RequestMeta{IP: "192.0.2.42"})
		require.NoError(t, err)
		var stored model.User
		require.NoError(t, q.Select("is_super,status").Where("tenant_id=? AND id=?", target.TenantID, user.ID).Take(&stored).Error)
		require.Equal(t, i == 0, stored.IsSuper)
		require.Equal(t, "active", stored.Status)
		var grants int64
		require.NoError(t, q.Model(&model.UserRole{}).Where("tenant_id=? AND user_id=? AND role_id=?", target.TenantID, user.ID, role.ID).Count(&grants).Error)
		require.EqualValues(t, 1, grants)
	}
	t.Log("§3.8 no active super: first unbound invitee becomes super; subsequent invitee stays ordinary; both retain preset role")
}

func TestIdentityTenantRecoveryIntegrationConcurrentFirstUsers(t *testing.T) {
	f := newTenantFixture(t)
	template := f.template(t, "concurrent-first")
	target := f.provision(t, template.ID, "concurrent-first-b")
	admin := recoveryAdmin(t, f, target.TenantID)
	accounts := recoveryAccounts(f)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, name := range []string{"simultaneous-a", "simultaneous-b"} {
		input := identity.AcceptInviteInput{Token: firstUserInvite(t, f, admin, name, nil), Password: accountPassword(t)}
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := accounts.AcceptInvite(f.ctx, target.TenantID, input, identity.RequestMeta{IP: "192.0.2.43"})
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Model(&model.User{}).Where("tenant_id=? AND status='active' AND is_super=true", target.TenantID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	t.Log("two distinct invitations accepted concurrently: both succeed, exactly one active super elected")
}

func TestIdentityTenantRecoveryIntegrationRemovedAdministrator(t *testing.T) {
	f := newTenantFixture(t)
	template := f.template(t, "removed-admin")
	target := f.provision(t, template.ID, "removed-admin-b")
	admin := recoveryAdmin(t, f, target.TenantID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Delete(&admin).Error)
	invite := recoveryInvite(t, f, target.TenantID, "", "")
	password := accountPassword(t)
	w, _ := f.request(t, "POST", "/auth/invite/accept", map[string]any{"tenant_id": target.TenantID, "username": "replacement-admin", "token": inviteRaw(t, invite.InviteURL), "password": password}, "")
	require.Equal(t, 201, w.Code)
	w, _ = f.request(t, "POST", "/auth/login", map[string]any{"tenant": "removed-admin-b", "account": "replacement-admin", "password": password}, "")
	require.Equal(t, 200, w.Code)
	require.True(t, recoveryAdmin(t, f, target.TenantID).IsSuper)
	t.Log("tenant with zero non-deleted users: recovery invite creates and activates its first administrator")
}

func firstUserInvite(t *testing.T, f *tenantFixture, creator model.User, username string, roles []int64) string {
	t.Helper()
	raw, err := refresh.Generate()
	require.NoError(t, err)
	if roles == nil {
		roles = []int64{}
	}
	grants, err := json.Marshal(roles)
	require.NoError(t, err)
	token := model.AuthToken{TenantID: creator.TenantID, Purpose: "invite", TokenHash: refresh.Hash(raw), DepartmentID: &creator.DepartmentID,
		Username: &username, MaxUses: 1, RoleIDs: grants, CreatedBy: creator.ID, CreatedAt: f.now, ExpiresAt: f.now.Add(time.Hour)}
	require.NoError(t, (&accountStore{f.identityFixture.store}).InsertToken(f.ctx, &token))
	return raw
}

func recoveryAccounts(f *tenantFixture) *identity.AccountService {
	return &identity.AccountService{Store: &accountStore{f.identityFixture.store}, Limits: f.redis, Now: func() time.Time { return f.now }}
}

type failingFirstUserStore struct{ *accountStore }

func (s *failingFirstUserStore) RememberPassword(context.Context, model.PasswordHistory) error {
	return context.Canceled
}

func TestIdentityTenantRecoveryIntegrationFirstUserRollback(t *testing.T) {
	f := newTenantFixture(t)
	template := f.template(t, "first-rollback")
	target := f.provision(t, template.ID, "first-rollback-b")
	admin := recoveryAdmin(t, f, target.TenantID)
	accounts := recoveryAccounts(f)
	input := identity.AcceptInviteInput{Token: firstUserInvite(t, f, admin, "first-rollback", nil), Password: accountPassword(t)}
	accounts.Store = &failingFirstUserStore{&accountStore{f.identityFixture.store}}
	_, err := accounts.AcceptInvite(f.ctx, target.TenantID, input, identity.RequestMeta{})
	require.Error(t, err)
	require.EqualValues(t, 1, f.count(t, "sys_user", target.TenantID))
	accounts.Store = &accountStore{f.identityFixture.store}
	_, err = accounts.AcceptInvite(f.ctx, target.TenantID, input, identity.RequestMeta{})
	require.NoError(t, err)
	t.Log("failed password history rolls back user/super grant/token consumption; retry elects first admin")
}

type waitingRecoveryStore struct {
	*accountStore
	started chan struct{}
}

func (s *waitingRecoveryStore) LockInviteTenant(ctx context.Context, tenant int64) error {
	close(s.started)
	return s.accountStore.LockInviteTenant(ctx, tenant)
}

func TestIdentityTenantRecoveryIntegrationReissueSerializesAcceptance(t *testing.T) {
	// Holding the issuer's lock proves an old invite cannot be consumed before revocation.
	f := newTenantFixture(t)
	template := f.template(t, "serialized-reissue")
	target := f.provision(t, template.ID, "serialized-reissue-b")
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	started := make(chan struct{})
	finished := make(chan error, 1)
	accounts := recoveryAccounts(f)
	accounts.Store = &waitingRecoveryStore{accountStore: &accountStore{f.identityFixture.store}, started: started}
	input := identity.AcceptInviteInput{Token: inviteRaw(t, target.InviteURL), Password: accountPassword(t)}
	err := f.database.Transaction(ctx, func(tx context.Context) error {
		if err := newTenantStore(f.database).LockInviteTenant(tx, target.TenantID); err != nil {
			return err
		}
		go func() {
			_, err := accounts.AcceptInvite(ctx, target.TenantID, input, identity.RequestMeta{IP: "192.0.2.44"})
			finished <- err
		}()
		select {
		case <-started:
		case <-ctx.Done():
			return ctx.Err()
		}
		_, err := f.tenants.ReissueAdminInvite(tx, f.actor, target.TenantID, false)
		return err
	})
	require.NoError(t, err)
	select {
	case err := <-finished:
		require.ErrorContains(t, err, "邀请或链接无效")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Equal(t, "disabled", recoveryAdmin(t, f, target.TenantID).Status)
	t.Log("in-flight acceptance waits for reissue commit, then rejects the old token; no account activation")
}

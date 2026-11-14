package bootstrap

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestIdentityRegistrationIntegrationConcurrentFirstUsers(t *testing.T) {
	f := newRegistrationFixture(t)
	f.settings(t, map[string]any{"allow_self_registration": true})
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET is_super=false WHERE tenant_id=1"))
	links := []string{
		f.register(t, registrationInput(t, "first-a"), 201).Link,
		f.register(t, registrationInput(t, "first-b"), 201).Link,
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var supers int64
	require.NoError(t, q.Model(&model.User{}).Where("tenant_id=1 AND is_super=true").Count(&supers).Error)
	require.Zero(t, supers)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, link := range links {
		raw := inviteRaw(t, link)
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- f.accounts.VerifyRegistration(f.ctx, raw, identity.RequestMeta{IP: "192.0.2.55"})
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.NoError(t, q.Model(&model.User{}).Where("tenant_id=1 AND is_super=true AND status='active'").Count(&supers).Error)
	require.EqualValues(t, 1, supers)
	t.Log("§4 #5: two disabled registrations verify concurrently; both activate; exactly one active super; no pre-verification super")
}

type registrationFailureStore struct {
	*accountStore
	insert   bool
	activate bool
}

func (s *registrationFailureStore) InsertToken(ctx context.Context, row *model.AuthToken) error {
	if s.insert {
		return context.Canceled
	}
	return s.accountStore.InsertToken(ctx, row)
}

func (s *registrationFailureStore) ActivateRegisteredUser(ctx context.Context, user model.User, roles []int64) error {
	if err := s.accountStore.ActivateRegisteredUser(ctx, user, roles); err != nil {
		return err
	}
	if s.activate {
		return context.Canceled
	}
	return nil
}

func TestIdentityRegistrationIntegrationRollbackAndRetry(t *testing.T) {
	f := newRegistrationFixture(t)
	f.settings(t, map[string]any{"allow_self_registration": true})
	store := &registrationFailureStore{accountStore: &accountStore{f.store}, insert: true}
	f.accounts.Store = store
	in := registrationInput(t, "rollback-registration")
	_, err := f.accounts.Register(f.ctx, in, identity.RequestMeta{})
	require.Error(t, err)
	require.EqualValues(t, 1, registrationCount(t, f, "sys_user", 1, 0))
	require.Zero(t, registrationCount(t, f, "password_history", 1, 0))
	require.Zero(t, registrationCount(t, f, "auth_token", 1, 0))
	store.insert = false
	result := f.register(t, in, 201)
	store.activate = true
	require.Error(t, f.accounts.VerifyRegistration(f.ctx, inviteRaw(t, result.Link), identity.RequestMeta{}))
	user := registrationUser(t, f, 1, in.Account)
	require.Equal(t, "disabled", user.Status)
	require.False(t, user.IsSuper)
	token, found, err := store.TokenByHash(f.ctx, 1, "verify", refresh.Hash(inviteRaw(t, result.Link)))
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, token.UsedAt)
	require.Equal(t, 0, token.UsedCount)
	require.Zero(t, registrationCount(t, f, "sys_user_role", 1, user.ID))
	store.activate = false
	f.verify(t, result.Link, 204)
	t.Log("token insertion failure rolls back user/password history; activation failure rolls back status/grants/consumption; same link retries successfully")
}

func TestIdentityRegistrationIntegrationExpiryAndPurposeSeparation(t *testing.T) {
	f := newRegistrationFixture(t)
	f.settings(t, map[string]any{"allow_self_registration": true})
	in := registrationInput(t, "expiring-registration")
	expired := f.register(t, in, 201)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE auth_token SET expires_at=? WHERE purpose='verify'", f.now))
	f.verify(t, expired.Link, 400)
	fresh := f.register(t, in, 202)
	invite := f.invite(t, 1)
	f.verify(t, invite.Link, 400)
	reset, err := f.accounts.ForgotPassword(f.ctx, 1, f.user.Username, identity.RequestMeta{})
	require.NoError(t, err)
	f.verify(t, reset.Link, 400)
	require.Error(t, f.accounts.ResetPassword(f.ctx, 1, inviteRaw(t, fresh.Link), accountPassword(t), identity.RequestMeta{}))
	_, err = f.accounts.AcceptInvite(f.ctx, 1, identity.AcceptInviteInput{Token: inviteRaw(t, fresh.Link), Username: "wrong-purpose", Password: accountPassword(t)}, identity.RequestMeta{})
	require.Error(t, err)
	f.verify(t, fresh.Link, 204)
	t.Log("24-hour boundary expired; resend renews; invite/reset cannot verify; verify cannot accept invite or reset password")
}

func TestIdentityRegistrationIntegrationConcurrentQuotaAndRedisFailure(t *testing.T) {
	f := newRegistrationFixture(t)
	start := make(chan struct{})
	allowed := make(chan bool, 8)
	failures := make(chan error, 8)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			ok, err := f.redis.AllowRegistration(f.ctx, 1, 2)
			allowed <- ok
			failures <- err
		}()
	}
	close(start)
	workers.Wait()
	close(allowed)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	count := 0
	for ok := range allowed {
		if ok {
			count++
		}
	}
	require.Equal(t, 2, count)
	f.server.FastForward(time.Hour)
	f.settings(t, map[string]any{"allow_self_registration": true})
	f.server.Close()
	w, out := f.call(t, "POST", "/auth/register", registrationInput(t, "redis-unavailable"))
	require.Equal(t, 503, w.Code)
	require.Equal(t, 10002, out.Code)
	require.EqualValues(t, 1, registrationCount(t, f, "sys_user", 1, 0))
	t.Log("eight simultaneous quota attempts allow exactly two; unavailable Redis fails closed with 503 and creates no user")
}

func TestIdentityRegistrationIntegrationPolicyChangesBeforeVerification(t *testing.T) {
	f := newRegistrationFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	role := model.Role{TenantID: 1, Code: "live-registration-role", Name: "reader", Status: "active", DataScope: "self"}
	require.NoError(t, q.Create(&role).Error)
	f.settings(t, map[string]any{"allow_self_registration": true, "default_role_code": role.Code})
	in := registrationInput(t, "policy-changes")
	result := f.register(t, in, 201)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_role SET status='disabled' WHERE id=?", role.ID))
	f.verify(t, result.Link, 409)
	require.Equal(t, "disabled", registrationUser(t, f, 1, in.Account).Status)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_role SET status='active' WHERE id=?", role.ID))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE tenant SET status='suspended' WHERE id=1"))
	f.verify(t, result.Link, 403)
	require.Equal(t, "disabled", registrationUser(t, f, 1, in.Account).Status)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE tenant SET status='active' WHERE id=1"))
	f.settings(t, map[string]any{"allow_self_registration": false})
	f.verify(t, result.Link, 204)
	user := registrationUser(t, f, 1, in.Account)
	require.Equal(t, "active", user.Status)
	var grants []model.UserRole
	require.NoError(t, q.Where("tenant_id=1 AND user_id=?", user.ID).Find(&grants).Error)
	require.Len(t, grants, 1)
	require.Equal(t, role.ID, grants[0].RoleID)
	t.Log("disabled default role and suspended tenant reject verification without consuming the link; closing registration still permits an issued link; activation uses the live tenant role")
}

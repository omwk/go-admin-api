package bootstrap

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestIdentityEmailChangeIntegrationConcurrentSingleUse(t *testing.T) {
	f, capture, _, next := emailChangeFixture(t)
	emailChangeRequest(t, f, next)
	emailChangeDeliver(t, f)
	raw := inviteRaw(t, capture.messages[0].Variables["Link"])
	start, results := make(chan struct{}), make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, _, err := f.accounts.VerifyEmailChange(f.ctx, raw, identity.RequestMeta{})
			status := 200
			if err != nil {
				status = errs.From(err).HTTPStatus
			}
			results <- status
		}()
	}
	close(start)
	require.ElementsMatch(t, []int{200, 400}, []int{<-results, <-results})
	t.Log("two concurrent consumers: exactly one 200 and one 400")
}

func TestIdentityEmailChangeIntegrationConcurrentRequests(t *testing.T) {
	f, _, old, next := emailChangeFixture(t)
	claims, err := f.service.Authenticate(f.ctx, f.token)
	require.NoError(t, err)
	start, results := make(chan struct{}), make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := f.accounts.ChangeEmail(f.ctx, claims, next, identity.RequestMeta{})
			results <- err
		}()
	}
	close(start)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var total, pending int64
	require.NoError(t, q.Table("auth_token").Where("purpose='email_change'").Count(&total).Error)
	require.NoError(t, q.Table("auth_token").Where("purpose='email_change' AND revoked_at IS NULL").Count(&pending).Error)
	require.EqualValues(t, 2, total)
	require.EqualValues(t, 1, pending)
	require.True(t, *accountUser(t, f, f.user.ID).Email == old)
}

func TestIdentityEmailChangeIntegrationWrongPurposeAndRecipient(t *testing.T) {
	f, capture, _, next := emailChangeFixture(t)
	reset, err := f.accounts.ForgotPassword(f.ctx, 1, f.user.Username, identity.RequestMeta{})
	require.NoError(t, err)
	emailChangeVerify(t, f, inviteRaw(t, reset.Link), 400)
	emailChangeRequest(t, f, next)
	first := accountOutbox(t, f, "identity.email_change")
	var in channel.Request
	require.NoError(t, channel.OpenJSON(f.notifications.sealer, "notification-outbox:v1:1:"+strconv.FormatInt(first.ID, 10), first.Payload, &in))
	_, err = f.notifications.prepareAccountMessage(f.ctx, 2, in)
	require.Error(t, err)
	bad := in
	bad.UserID++
	_, err = f.notifications.prepareAccountMessage(f.ctx, 1, bad)
	require.Error(t, err)
	emailChangeRequest(t, f, next)
	_, err = f.notifications.prepareAccountMessage(f.ctx, 1, in)
	require.Error(t, err)
	d, err := f.notifications.delivery.Deliver(f.ctx, 1, first.ID)
	require.NoError(t, err)
	require.Equal(t, "message_unavailable", d.LastError)
	require.Empty(t, capture.messages)
	emailChangeDeliver(t, f)
	raw := inviteRaw(t, capture.messages[0].Variables["Link"])
	emailChangeVerify(t, f, raw, 200)
	_, err = f.notifications.prepareAccountMessage(f.ctx, 1, in)
	require.Error(t, err)
}

type emailChangeFailingStore struct{ *accountStore }

func (*emailChangeFailingStore) SaveToken(context.Context, model.AuthToken) error {
	return errors.New("injected token persistence failure")
}

func TestIdentityEmailChangeIntegrationRollbackAndCache(t *testing.T) {
	f, capture, old, next := emailChangeFixture(t)
	store := f.accounts.Store.(*accountStore)
	versions := authz.New(f.redis.cache)
	profilesStore := newProfileStore(f.database)
	profiles, err := identity.NewProfileService(profilesStore)
	require.NoError(t, err)
	profiles.UseAuthz(versions, profilesStore.account)
	before, err := profiles.Me(f.ctx, 1, f.user.ID)
	require.NoError(t, err)
	require.True(t, *before.User.Email == old)
	invalidations := 0
	f.accounts.Invalidate = func(ctx context.Context, tenant int64, users []int64) bool {
		require.False(t, db.InTransaction(ctx))
		invalidations++
		return versions.Pending(ctx, tenant, users)
	}
	emailChangeRequest(t, f, next)
	emailChangeDeliver(t, f)
	raw := inviteRaw(t, capture.messages[0].Variables["Link"])
	f.accounts.Store = &emailChangeFailingStore{store}
	emailChangeVerify(t, f, raw, 503)
	require.Zero(t, invalidations)
	require.True(t, *accountUser(t, f, f.user.ID).Email == old)
	row, found, err := store.TokenByHash(f.ctx, 1, "email_change", refresh.Hash(raw))
	require.NoError(t, err)
	require.True(t, found && row.Valid(f.now))
	f.accounts.Store = store
	emailChangeVerify(t, f, raw, 200)
	require.Equal(t, 1, invalidations)
	after, err := profiles.Me(f.ctx, 1, f.user.ID)
	require.NoError(t, err)
	require.True(t, *after.User.Email == next)
}

func TestIdentityEmailChangeIntegrationNoticeAfterVerify(t *testing.T) {
	f, capture, old, next := emailChangeFixture(t)
	emailChangeRequest(t, f, next)
	out := accountOutbox(t, f, "identity.email_change")
	d, err := f.notifications.delivery.Deliver(f.ctx, 1, out.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	emailChangeVerify(t, f, inviteRaw(t, capture.messages[0].Variables["Link"]), 200)
	out = accountOutbox(t, f, "identity.email_change_notice")
	d, err = f.notifications.delivery.Deliver(f.ctx, 1, out.ID)
	require.NoError(t, err)
	require.Equal(t, "sent", d.Status)
	require.Len(t, capture.messages, 2)
	require.True(t, capture.messages[1].Recipient == old)
	require.Empty(t, capture.messages[1].Variables["Link"])
}

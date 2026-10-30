package bootstrap

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/session"
	"github.com/stretchr/testify/require"
)

func TestIdentityIntegrationRotationGraceAndDeviceIsolation(t *testing.T) {
	// REQ-IAM-002/010: persisted rotation time controls all grace/replay decisions.
	f := newIdentityFixture(t)
	pc, phone := f.login(t), f.login(t)
	rotatedAt := f.now
	next, err := f.service.Refresh(f.ctx, pc.RefreshToken, identity.RequestMeta{})
	require.NoError(t, err)
	old, found, err := f.store.RefreshByHash(f.ctx, refresh.Hash(pc.RefreshToken))
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, old.ReplacedByTokenID)
	require.Equal(t, rotatedAt, old.RevokedAt.UTC())
	require.True(t, f.server.Exists(session.RefreshKey(old.TokenHash)))
	for _, seconds := range []int{0, 29, 30} {
		f.now = rotatedAt.Add(time.Duration(seconds) * time.Second)
		_, err = f.service.Refresh(f.ctx, pc.RefreshToken, identity.RequestMeta{})
		require.Equal(t, 20109, errs.From(err).Code)
		row, _, err := f.store.RefreshByHash(f.ctx, old.TokenHash)
		require.NoError(t, err)
		require.Equal(t, rotatedAt, row.RevokedAt.UTC())
	}
	f.now = rotatedAt.Add(31 * time.Second)
	_, err = f.service.Refresh(f.ctx, pc.RefreshToken, identity.RequestMeta{})
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.service.Refresh(f.ctx, next.RefreshToken, identity.RequestMeta{})
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.service.Authenticate(f.ctx, next.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.service.Refresh(f.ctx, phone.RefreshToken, identity.RequestMeta{})
	require.NoError(t, err)
}

func TestIdentityIntegrationConcurrentRefreshOnlyOneSucceeds(t *testing.T) {
	f := newIdentityFixture(t)
	issued := f.login(t)
	start := make(chan struct{})
	results := make(chan error, 8)
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := f.service.Refresh(f.ctx, issued.RefreshToken, identity.RequestMeta{})
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			require.Equal(t, 20109, errs.From(err).Code)
		}
	}
	require.Equal(t, 1, winners)
	old, _, err := f.store.RefreshByHash(f.ctx, refresh.Hash(issued.RefreshToken))
	require.NoError(t, err)
	family, err := f.store.Family(f.ctx, old.TenantID, old.UserID, old.SessionID)
	require.NoError(t, err)
	require.Len(t, family.Tokens, 2)
}

func TestIdentityIntegrationLogoutSurvivesRedisFailureAndDataLoss(t *testing.T) {
	f := newIdentityFixture(t)
	pc, phone := f.login(t), f.login(t)
	next, err := f.service.Refresh(f.ctx, pc.RefreshToken, identity.RequestMeta{})
	require.NoError(t, err)
	claims, err := f.service.Authenticate(f.ctx, next.AccessToken)
	require.NoError(t, err)
	f.writes.fail = true
	err = f.service.Logout(f.ctx, claims)
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	family, err := f.store.Family(f.ctx, claims.TenantID, f.user.ID, claims.SessionID)
	require.NoError(t, err)
	for _, token := range family.Tokens {
		require.Equal(t, "logout", *token.RevokedReason)
		require.NotNil(t, token.RevokedAt)
	}
	f.writes.fail = false
	f.server.FlushAll()
	_, err = f.service.Authenticate(f.ctx, pc.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.service.Refresh(f.ctx, pc.RefreshToken, identity.RequestMeta{})
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.service.Refresh(f.ctx, next.RefreshToken, identity.RequestMeta{})
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.service.Authenticate(f.ctx, phone.AccessToken)
	require.NoError(t, err)
}

func TestIdentityIntegrationRefreshWriteFailureAndLoginAudits(t *testing.T) {
	f := newIdentityFixture(t)
	wrong, err := refresh.Generate()
	require.NoError(t, err)
	_, err = f.service.Login(f.ctx, 1, f.user.Username, wrong, identity.RequestMeta{})
	require.Equal(t, 20101, errs.From(err).Code)
	issued := f.login(t)
	f.writes.fail = true
	result, err := f.service.Refresh(f.ctx, issued.RefreshToken, identity.RequestMeta{})
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	require.Equal(t, identity.Tokens{}, result)
	old, _, err := f.store.RefreshByHash(f.ctx, refresh.Hash(issued.RefreshToken))
	require.NoError(t, err)
	require.Equal(t, "rotated", *old.RevokedReason)
	require.NotNil(t, old.ReplacedByTokenID)
	family, err := f.store.Family(f.ctx, old.TenantID, old.UserID, old.SessionID)
	require.NoError(t, err)
	require.Len(t, family.Tokens, 2)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var records []map[string]any
	require.NoError(t, q.Table("audit_log").Select("actor_id, action, resource_id, result, trace_id, before, after").Order("id").Find(&records).Error)
	require.Len(t, records, 2)
	require.Equal(t, "failure", records[0]["result"])
	require.Equal(t, "success", records[1]["result"])
	encoded, err := json.Marshal(records)
	require.NoError(t, err)
	for _, sensitive := range []string{f.password, wrong, issued.AccessToken, issued.RefreshToken} {
		require.False(t, strings.Contains(string(encoded), sensitive))
	}
}

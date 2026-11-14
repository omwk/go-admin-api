package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

type sessionAccountStub struct{ profileAccountStub }

func (s *sessionAccountStub) SessionIDs(ctx context.Context, tenant, user int64) ([]string, error) {
	defer s.memory.read(ctx)()
	unique := map[string]bool{}
	for _, row := range s.memory.tokens {
		if row.TenantID == tenant && row.UserID == user {
			unique[row.SessionID] = true
		}
	}
	ids := make([]string, 0, len(unique))
	for sid := range unique {
		ids = append(ids, sid)
	}
	sort.Strings(ids)
	return ids, nil
}

func logoutOthersRequest(t *testing.T, f *authFixture, token string) *httptest.ResponseRecorder {
	t.Helper()
	accounts := &AccountService{Store: &sessionAccountStub{profileAccountStub{memory: f.store}}, Auth: f.service, Now: func() time.Time { return f.now }}
	router := gin.New()
	RegisterAccountSelfRoutes(router, f.service, accounts)
	request := httptest.NewRequest("POST", "/api/v1/auth/logout-others", nil).WithContext(f.ctx)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func requireLogoutOthersCount(t *testing.T, response *httptest.ResponseRecorder, count int) {
	t.Helper()
	require.Equal(t, 200, response.Code, response.Body.String())
	var envelope struct {
		Code int            `json:"code"`
		Data map[string]int `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Zero(t, envelope.Code)
	require.Equal(t, map[string]int{"revoked": count}, envelope.Data)
}

func TestAccountLogoutOthersKeepsCurrentAndCountsFamilies(t *testing.T) {
	// BE-IDN-005 §3 #1-3/7: rotation history counts once and repeat calls audit zero.
	f := newAuthFixture(t)
	current, first, second := f.login(t), f.login(t), f.login(t)
	rotated, err := f.service.Refresh(f.ctx, first.RefreshToken, RequestMeta{})
	require.NoError(t, err)
	requireLogoutOthersCount(t, logoutOthersRequest(t, f, current.AccessToken), 2)
	_, err = f.service.Authenticate(f.ctx, current.AccessToken)
	require.NoError(t, err)
	kept, err := f.service.Refresh(f.ctx, current.RefreshToken, RequestMeta{})
	require.NoError(t, err)
	_, err = f.service.Authenticate(f.ctx, kept.AccessToken)
	require.NoError(t, err)
	for _, tokens := range []Tokens{first, rotated, second} {
		_, err = f.service.Authenticate(f.ctx, tokens.AccessToken)
		require.Equal(t, 20103, errs.From(err).Code)
		_, err = f.service.Refresh(f.ctx, tokens.RefreshToken, RequestMeta{})
		require.Equal(t, 20103, errs.From(err).Code)
	}
	requireLogoutOthersCount(t, logoutOthersRequest(t, f, current.AccessToken), 0)
	var events []audit.Event
	for _, event := range f.store.audits {
		if event.Action == "system:user:logout_others" {
			events = append(events, event)
		}
	}
	require.Len(t, events, 2)
	for i, count := range []int{2, 0} {
		require.Equal(t, map[string]any{"revoked": count}, events[i].After)
		require.Empty(t, events[i].Before)
		encoded, err := json.Marshal(events[i])
		require.NoError(t, err)
		for _, row := range f.store.tokens {
			require.False(t, strings.Contains(string(encoded), row.SessionID), "audit must not contain a session ID")
		}
	}
}

func TestAccountLogoutOthersAuditFailureRollsBack(t *testing.T) {
	// BE-IDN-005: audit and persistent revocation must roll back together.
	f := newAuthFixture(t)
	current, other := f.login(t), f.login(t)
	f.service.audit = func(ctx context.Context, event audit.Event) error {
		require.Equal(t, true, ctx.Value(fakeTxKey{}))
		require.Equal(t, "system:user:logout_others", event.Action)
		require.Equal(t, map[string]any{"revoked": 1}, event.After)
		return errors.New("injected audit failure")
	}
	require.Equal(t, 503, logoutOthersRequest(t, f, current.AccessToken).Code)
	for _, tokens := range []Tokens{current, other} {
		_, err := f.service.Authenticate(f.ctx, tokens.AccessToken)
		require.NoError(t, err)
	}
	require.Empty(t, f.redis.values)
}

func TestAccountLogoutOthersRedisFailurePreservesPersistentRevocation(t *testing.T) {
	// BE-IDN-005: Redis failure cannot undo the committed revocation or audit.
	f := newAuthFixture(t)
	current, other := f.login(t), f.login(t)
	f.redis.failMark = true
	require.Equal(t, 503, logoutOthersRequest(t, f, current.AccessToken).Code)
	f.redis.failMark = false
	_, err := f.service.Authenticate(f.ctx, current.AccessToken)
	require.NoError(t, err)
	_, err = f.service.Authenticate(f.ctx, other.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	require.Equal(t, "system:user:logout_others", f.store.audits[len(f.store.audits)-1].Action)
	requireLogoutOthersCount(t, logoutOthersRequest(t, f, current.AccessToken), 0)
}

func TestAccountLogoutOthersIncludesExpiredAndExcludesRevokedCounts(t *testing.T) {
	// BE-IDN-005 §2: expired families are revoked; terminal families do not inflate the count.
	f := newAuthFixture(t)
	current, expired, loggedOut := f.login(t), f.login(t), f.login(t)
	expiredClaims, err := f.service.Authenticate(f.ctx, expired.AccessToken)
	require.NoError(t, err)
	claims, err := f.service.Authenticate(f.ctx, loggedOut.AccessToken)
	require.NoError(t, err)
	require.NoError(t, f.service.Logout(f.ctx, claims))
	for id, row := range f.store.tokens {
		if row.SessionID == expiredClaims.SessionID {
			row.ExpiresAt = f.now.Add(-time.Hour)
			f.store.tokens[id] = row
		}
	}
	requireLogoutOthersCount(t, logoutOthersRequest(t, f, current.AccessToken), 1)
	family, err := f.store.Family(f.ctx, 1, f.store.user.ID, expiredClaims.SessionID)
	require.NoError(t, err)
	require.True(t, familyRevoked(family))
	requireLogoutOthersCount(t, logoutOthersRequest(t, f, current.AccessToken), 0)
}

func TestAccountLogoutOthersRequiresAuthentication(t *testing.T) {
	f := newAuthFixture(t)
	require.Equal(t, 401, logoutOthersRequest(t, f, "").Code)
	require.Empty(t, f.store.audits)
}

func TestAccountLogoutOthersRejectsInvalidClaims(t *testing.T) {
	f := newAuthFixture(t)
	current := f.login(t)
	claims, err := f.service.Authenticate(f.ctx, current.AccessToken)
	require.NoError(t, err)
	accounts := &AccountService{Store: &sessionAccountStub{profileAccountStub{memory: f.store}}, Auth: f.service, Now: func() time.Time { return f.now }}
	for name, invalid := range map[string]jwt.Claims{
		"empty session": {TenantID: 1, Subject: claims.Subject},
		"invalid user":  {TenantID: 1, Subject: "invalid", SessionID: claims.SessionID},
		"other tenant":  {TenantID: 2, Subject: claims.Subject, SessionID: claims.SessionID},
	} {
		t.Run(name, func(t *testing.T) {
			count, err := accounts.LogoutOthers(f.ctx, invalid)
			require.Equal(t, 20103, errs.From(err).Code)
			require.Zero(t, count)
		})
	}
	_, err = f.service.Authenticate(f.ctx, current.AccessToken)
	require.NoError(t, err)
}

var _ repo.Account = (*sessionAccountStub)(nil)

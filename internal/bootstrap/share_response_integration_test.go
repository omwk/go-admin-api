package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func TestShareIntegrationDeniedTokenResponsesIndistinguishable(t *testing.T) {
	// REQ-SHR-003: default CI checks observable denials without timing assertions.
	f := newShareAccessFixture(t)
	in := f.publication()
	expires := f.now.Add(time.Minute)
	in.ExpiresAt = &expires
	expired := f.issue(t, in)
	revoked := f.issue(t, f.publication())
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row share.Link
	require.NoError(t, q.Where("id=?", revoked.ID).Take(&row).Error)
	foreign, _ := f.samePrefixTenantLink(t, revoked, row)
	r, _ := f.request(t, "POST", fmt.Sprintf("/share-links/%d/revoke", revoked.ID), nil)
	require.Equal(t, http.StatusNoContent, r.Code)
	// The foreign link is active and usable in its own server-selected tenant.
	f.now = expires.Add(time.Second)
	f.router = gin.New()
	share.RegisterPublicRoutes(f.router, f.links, 2)
	require.Equal(t, http.StatusOK, f.anonymous(t, "GET", foreign.URL, "192.0.2.1", nil, nil).Code)
	f.router = gin.New()
	share.RegisterPublicRoutes(f.router, f.links, 1)
	paths := []struct{ name, path string }{
		{"invalid", "/api/v1/share/" + randomShareProbe(t)},
		{"expired", expired.URL},
		{"revoked", revoked.URL},
		{"unauthorized_tenant", foreign.URL},
	}
	wantHeaders := http.Header{
		"Cache-Control":          {"private, no-store"},
		"Content-Type":           {"application/json; charset=utf-8"},
		"Referrer-Policy":        {"no-referrer"},
		"X-Content-Type-Options": {"nosniff"},
	}
	wantBody := `{"code":70102,"message":"链接暂不可用","data":null,"trace_id":""}`
	for _, state := range paths {
		t.Run(state.name, func(t *testing.T) {
			w := f.anonymous(t, "GET", state.path, "192.0.2.1", nil, nil)
			require.Equal(t, http.StatusNotFound, w.Code)
			require.Equal(t, wantHeaders, w.Header(), "all response headers must match")
			require.Equal(t, wantBody, w.Body.String(), "complete denial bodies must match")
			var body map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			keys := make([]string, 0, len(body))
			for key := range body {
				keys = append(keys, key)
			}
			require.ElementsMatch(t, []string{"code", "message", "data", "trace_id"}, keys)
			require.JSONEq(t, wantBody, w.Body.String())
		})
	}
}

type shareBudgetCall struct {
	key   string
	rate  float64
	burst int
}

type shareObservedCache struct {
	share.AccessCache
	calls []shareBudgetCall
}

func (c *shareObservedCache) TokenBucket(ctx context.Context, key string, rate float64, burst int) (bool, int, time.Duration, error) {
	c.calls = append(c.calls, shareBudgetCall{key, rate, burst})
	return c.AccessCache.TokenBucket(ctx, key, rate, burst)
}

func TestShareIntegrationEntryLimiterCoversInvalidAndValidTokens(t *testing.T) {
	// REQ-SHR-003: observe the real limiter, including both paths after exhaustion.
	f := newShareAccessFixture(t)
	link := f.issue(t, f.publication())
	cached := &shareObservedCache{AccessCache: f.redis.cache}
	svc, err := share.NewLinkService(share.LinkDependencies{Store: &shareStore{database: f.database}, Cache: cached,
		Sealer: f.sealer, Idempotency: newIdempotencyStore(f.database), Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	f.router = gin.New()
	share.RegisterPublicRoutes(f.router, svc, 1)
	invalid := "/api/v1/share/" + randomShareProbe(t)
	require.Equal(t, http.StatusNotFound, f.anonymous(t, "GET", invalid, "192.0.2.1", nil, nil).Code)
	require.Len(t, cached.calls, 2)
	entry := append([]shareBudgetCall(nil), cached.calls...)
	require.Equal(t, shareBudgetCall{"share:entry:all:v1:1", 1000, 2000}, entry[1])
	require.Contains(t, entry[0].key, "share:entry:v1:1:")
	require.Equal(t, float64(10), entry[0].rate)
	require.Equal(t, 60, entry[0].burst)
	cached.calls = nil
	require.Equal(t, http.StatusOK, f.anonymous(t, "GET", link.URL, "192.0.2.1", nil, nil).Code)
	require.Len(t, cached.calls, 4)
	require.Equal(t, entry, cached.calls[:2], "both paths must enter identical IP and tenant budgets")
	for range 58 {
		allowed, _, _, err := f.redis.cache.TokenBucket(f.ctx, entry[0].key, entry[0].rate, entry[0].burst)
		require.NoError(t, err)
		require.True(t, allowed)
	}
	cached.calls = nil
	for _, path := range []string{invalid, link.URL} {
		w := f.anonymous(t, "GET", path, "192.0.2.1", nil, nil)
		require.Equal(t, http.StatusTooManyRequests, w.Code)
		require.Equal(t, "60", w.Header().Get("Retry-After"))
		require.JSONEq(t, `{"code":10005,"message":"链接暂不可用","data":null,"trace_id":""}`, w.Body.String())
	}
	require.Equal(t, []shareBudgetCall{entry[0], entry[0]}, cached.calls)
}

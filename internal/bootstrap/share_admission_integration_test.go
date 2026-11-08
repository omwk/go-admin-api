package bootstrap

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShareIntegrationMaxViewsNthAndDetailDoesNotSpend(t *testing.T) {
	f := newShareAccessFixture(t)
	in := f.publication()
	in.MaxViews = 3
	link := f.issue(t, in)
	var last *httptest.ResponseRecorder
	for range 3 {
		last = f.anonymous(t, "GET", link.URL, "192.0.2.1", nil, nil)
		require.Equal(t, 200, last.Code)
	}
	cookie := shareCookie(t, last)
	require.Equal(t, 404, f.anonymous(t, "GET", link.URL, "192.0.2.1", nil, cookie).Code)
	for range 5 {
		require.Equal(t, 200, f.anonymous(t, "GET", fmt.Sprintf("%s/items/%d", link.URL, f.spu), "192.0.2.1", nil, cookie).Code)
	}
	require.Equal(t, 404, f.anonymous(t, "GET", fmt.Sprintf("%s/items/%d", link.URL, f.spu+10000), "192.0.2.1", nil, cookie).Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int
	require.NoError(t, q.Table("share_link").Select("view_count").Where("id=?", link.ID).Take(&count).Error)
	require.Equal(t, 3, count)
}

func TestShareIntegrationConcurrentMaxViewsNoOversell(t *testing.T) {
	// REQ-SHR-005: one link row is the serial point for all admitted root requests.
	f := newShareAccessFixture(t)
	in := f.publication()
	in.MaxViews = 10
	link := f.issue(t, in)
	responses := make(chan int, 100)
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "https://quote.test"+link.URL, nil)
			req.RemoteAddr = fmt.Sprintf("192.0.2.%d:32000", index+1)
			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, req)
			responses <- w.Code
		}(i)
	}
	wg.Wait()
	close(responses)
	success := 0
	for code := range responses {
		if code == 200 {
			success++
		} else {
			require.Equal(t, 404, code)
		}
	}
	require.Equal(t, 10, success)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int
	require.NoError(t, q.Table("share_link").Select("view_count").Where("id=?", link.ID).Take(&count).Error)
	require.Equal(t, 10, count)
}

func TestShareIntegrationWarmCacheRevocationAndParentStatus(t *testing.T) {
	f := newShareAccessFixture(t)
	link := f.issue(t, f.publication())
	w := f.anonymous(t, "GET", link.URL, "192.0.2.1", nil, nil)
	require.Equal(t, 200, w.Code)
	cookie := shareCookie(t, w)
	key := fmt.Sprintf("share:content:v1:1:%d:1", link.ID)
	_, found, err := f.redis.cache.Get(f.ctx, key)
	require.NoError(t, err)
	require.True(t, found)
	r, _ := f.request(t, "POST", fmt.Sprintf("/share-links/%d/revoke", link.ID), nil)
	require.Equal(t, 204, r.Code)
	for _, path := range []string{link.URL, fmt.Sprintf("%s/items/%d", link.URL, f.spu), link.URL + "/verify"} {
		method := "GET"
		if path == link.URL+"/verify" {
			method = "POST"
		}
		require.Equal(t, 404, f.anonymous(t, method, path, "192.0.2.1", map[string]string{"password": ""}, cookie).Code)
	}
	_, found, err = f.redis.cache.Get(f.ctx, key)
	require.NoError(t, err)
	require.True(t, found, "revocation refuses even the retained, previously valid cache entry")
}

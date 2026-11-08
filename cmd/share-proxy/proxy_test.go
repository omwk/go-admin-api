package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicConfigurationRejectsSystemSurfaceBeforeUpstream(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		require.Equal(t, "quote.test", r.Host)
		require.Equal(t, "192.0.2.30", r.Header.Get("X-Forwarded-For"))
		require.Empty(t, r.Header.Get("X-Real-IP"))
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	config, err := os.ReadFile("../../deploy/share-public.json")
	require.NoError(t, err)
	handler, err := publicProxy(target, []string{"127.0.0.1"}, strings.NewReader(string(config)))
	require.NoError(t, err)
	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/api/v1/admin/system/info", "/api/v1/auth/login"} {
		for _, suffix := range []string{"", "?x=1", "/", "%2f"} {
			for _, method := range []string{"GET", "POST", "OPTIONS", "HEAD"} {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(method, "https://quote.test"+path+suffix, nil))
				require.Equal(t, 404, w.Code, method+" "+path+suffix)
				require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
			}
		}
	}
	require.Zero(t, hits.Load())
	root := "/api/v1/share/" + strings.Repeat("x", 24)
	for _, route := range []struct{ method, path string }{{"GET", root}, {"POST", root + "/verify"}, {"POST", root + "/inquiry"}, {"GET", root + "/items/1"}} {
		r := httptest.NewRequest(route.method, "https://quote.test"+route.path, nil)
		r.RemoteAddr = "127.0.0.1:8082"
		r.Header.Set("X-Forwarded-For", "203.0.113.9, 192.0.2.30")
		r.Header.Set("X-Real-IP", "203.0.113.9")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		require.Equal(t, 204, w.Code)
	}
	require.EqualValues(t, 4, hits.Load())
	for _, path := range []string{root + "/inquiry/1", root + "?x=1", strings.Replace(root, "/share/", "/%73hare/", 1)} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "https://quote.test"+path, nil))
		require.Equal(t, 404, w.Code)
	}
	t.Log("public allowlist: healthz/readyz/metrics/system-info=404 before upstream; four exact share routes forwarded; source chain constrained")
}

func TestPublicProxyDependencyFailureDoesNotLeakURI(t *testing.T) {
	config, err := os.ReadFile("../../deploy/share-public.json")
	require.NoError(t, err)
	target, err := url.Parse("http://127.0.0.1:1")
	require.NoError(t, err)
	handler, err := publicProxy(target, nil, strings.NewReader(string(config)))
	require.NoError(t, err)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "https://quote.test/api/v1/share/"+strings.Repeat("x", 24), nil))
	require.Equal(t, 503, w.Code)
	require.Equal(t, "链接暂不可用\n", w.Body.String())
}

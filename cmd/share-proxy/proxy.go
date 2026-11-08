package main

import (
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

func publicProxy(target *url.URL, trusted []string, allowlist io.Reader) (http.Handler, error) {
	source, err := httpx.NewSourceResolver(trusted)
	if err != nil {
		return nil, err
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.Out.Host = r.In.Host
		r.SetXForwarded()
		r.Out.Header.Del("X-Real-IP")
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { http.Error(w, "链接暂不可用", 503) }, ErrorLog: log.New(io.Discard, "", 0)}
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded := r.Clone(r.Context())
		forwarded.RemoteAddr = net.JoinHostPort(source(r), "0")
		proxy.ServeHTTP(w, forwarded)
	})
	return httpx.PublicIngress(upstream, allowlist)
}

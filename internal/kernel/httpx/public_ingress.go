package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// PublicRoute is one explicitly published method and anchored path expression.
type PublicRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// PublicIngress loads the reverse proxy's allowlist and rejects before upstream access.
func PublicIngress(next http.Handler, reader io.Reader) (http.Handler, error) {
	var config struct {
		Routes []PublicRoute `json:"routes"`
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (16<<10)+1))
	if err != nil || len(raw) > 16<<10 {
		return nil, errors.New("public ingress configuration exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(config.Routes) == 0 {
		return nil, errors.New("public ingress requires an explicit route allowlist")
	}
	patterns := make([]*regexp.Regexp, len(config.Routes))
	for i, route := range config.Routes {
		if !strings.HasPrefix(route.Path, "^/api/v1/share/") || !strings.HasSuffix(route.Path, "$") || route.Method != "GET" && route.Method != "POST" {
			return nil, errors.New("public ingress route outside share boundary")
		}
		var err error
		patterns[i], err = regexp.Compile(route.Path)
		if err != nil {
			return nil, errors.New("invalid public ingress route")
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		path, err := url.PathUnescape(r.URL.EscapedPath())
		if err == nil && strings.HasPrefix(path, "/api/v1/share/") && path == r.URL.Path && !strings.ContainsAny(r.URL.EscapedPath(), "%\\") && r.URL.RawQuery == "" {
			for i, route := range config.Routes {
				if r.Method == route.Method && patterns[i].MatchString(path) {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		http.NotFound(w, r)
	}), nil
}

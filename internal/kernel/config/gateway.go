package config

import (
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Gateway holds the public origin, CORS allowlist and shared rate limits.
type Gateway struct {
	PublicBaseURL  string   `mapstructure:"public_base_url"`
	AllowedOrigins []string `mapstructure:"allowed_origins"`
	GlobalQPS      int      `mapstructure:"global_qps"`
	GlobalBurst    int      `mapstructure:"global_burst"`
	ClientQPS      int      `mapstructure:"client_qps"`
	ClientBurst    int      `mapstructure:"client_burst"`
	RouteQPS       int      `mapstructure:"route_qps"`
	RouteBurst     int      `mapstructure:"route_burst"`
}

// DefaultGateway supplies explicit policies without deriving QPS from call quotas.
func DefaultGateway() Gateway {
	return Gateway{GlobalQPS: 5000, GlobalBurst: 10000, ClientQPS: 10, ClientBurst: 20, RouteQPS: 1000, RouteBurst: 2000}
}

func (g *Gateway) defaultPublicBaseURL(env string) {
	if strings.EqualFold(env, "dev") && strings.TrimSpace(g.PublicBaseURL) == "" {
		g.PublicBaseURL = "http://127.0.0.1:18080"
		slog.Warn("gateway.public_base_url is unset; using development default", "module", "config", "action", "default_gateway_public_base_url")
	}
}

func (g Gateway) validate() error {
	const originRule = "must be an HTTP(S) origin without path, credentials, query or fragment"
	if strings.TrimSpace(g.PublicBaseURL) == "" {
		return fmt.Errorf("gateway.public_base_url is required outside dev")
	}
	if !validHTTPOrigin(g.PublicBaseURL) {
		return fmt.Errorf("gateway.public_base_url %s", originRule)
	}
	for i, origin := range g.AllowedOrigins {
		if !validHTTPOrigin(origin) {
			return fmt.Errorf("gateway.allowed_origins[%d] %s", i, originRule)
		}
	}
	return nil
}

func validHTTPOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return false
	}
	if port := u.Port(); port != "" {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return false
		}
	}
	return !strings.HasSuffix(u.Host, ":") && validOriginHost(u.Host, u.Hostname())
}

var originHost = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validOriginHost(authority, host string) bool {
	if strings.HasPrefix(authority, "[") {
		address, err := netip.ParseAddr(host)
		return err == nil && address.Is6() && address.Zone() == ""
	}
	return originHost.MatchString(host)
}

package httpx

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// NewSourceResolver accepts forwarding only from explicitly configured proxy addresses.
// Its result is used for transient admission budgets, never persisted or logged.
func NewSourceResolver(proxies []string) (func(*http.Request) string, error) {
	prefixes := make([]netip.Prefix, 0, len(proxies))
	for _, value := range proxies {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			ip, parseErr := netip.ParseAddr(value)
			if parseErr != nil {
				return nil, errors.New("invalid trusted proxy address")
			}
			prefix = netip.PrefixFrom(ip, ip.BitLen())
		}
		if prefix.Bits() == 0 {
			return nil, errors.New("trusted proxies cannot include every address")
		}
		prefixes = append(prefixes, prefix)
	}
	trusted := func(ip netip.Addr) bool {
		for _, prefix := range prefixes {
			if prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	return func(r *http.Request) string {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		peer, err := netip.ParseAddr(host)
		if err != nil || !trusted(peer) {
			return host
		}
		chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(chain) - 1; i >= 0; i-- {
			ip, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
			if err != nil {
				return host
			}
			if i == 0 || !trusted(ip) {
				return ip.String()
			}
		}
		return host
	}, nil
}

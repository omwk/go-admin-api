package httpx

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrustedProxySourceChain(t *testing.T) {
	for _, tc := range []struct {
		name, remote, forwarded, want string
		trusted                       []string
	}{
		{"default ignores spoof", "192.0.2.1:80", "198.51.100.2", "192.0.2.1", nil},
		{"untrusted peer", "192.0.2.1:80", "198.51.100.2", "192.0.2.1", []string{"127.0.0.1"}},
		{"explicit proxy", "127.0.0.1:80", "198.51.100.2", "198.51.100.2", []string{"127.0.0.1"}},
		{"spoof before client", "127.0.0.1:80", "203.0.113.8, 198.51.100.2", "198.51.100.2", []string{"127.0.0.1"}},
		{"trusted chain", "127.0.0.1:80", "198.51.100.2, 10.1.2.3", "198.51.100.2", []string{"127.0.0.1", "10.1.2.0/24"}},
		{"invalid chain", "127.0.0.1:80", "198.51.100.2, invalid", "127.0.0.1", []string{"127.0.0.1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolve, err := NewSourceResolver(tc.trusted)
			require.NoError(t, err)
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			r.Header.Set("X-Forwarded-For", tc.forwarded)
			r.Header.Set("X-Real-IP", "203.0.113.10")
			require.Equal(t, tc.want, resolve(r))
		})
	}
	for _, value := range []string{"*", "0.0.0.0/0", "::/0", "invalid"} {
		_, err := NewSourceResolver([]string{value})
		require.Error(t, err)
	}
}

package gateway

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalRequest(t *testing.T) {
	// REQ-GW-003: query normalization and the original escaped path are protocol facts.
	for _, tc := range []struct{ name, target, query string }{
		{"empty", "/open/v1/orders/test/status", ""},
		{"sorted empty", "/open/v1/orders/test/status?z=&a=2", "a=2&z="},
		{"space plus percent", "/open/v1/orders/test/status?q=a%20b%2B%25", "q=a+b%2B%25"},
		{"unicode", "/open/v1/orders/%E8%AE%A2%E5%8D%95/status?q=%E4%B8%AD%E6%96%87", "q=%E4%B8%AD%E6%96%87"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.target, nil)
			got, err := CanonicalRequest(r, "1800000000", strings.Repeat("a", 32), nil)
			require.NoError(t, err)
			want := fmt.Sprintf("GET\n%s\n%s\n1800000000\n%s\n%x", r.URL.EscapedPath(), tc.query, strings.Repeat("a", 32), sha256.Sum256(nil))
			require.Equal(t, want, got)
		})
	}
}

func TestCanonicalRequestRejectsAmbiguousQuery(t *testing.T) {
	for _, query := range []string{"a=1&a=2", "a=1&%61=2", "q=%zz", "a=1;b=2", "bad%0Akey=x"} {
		t.Run(query, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/open/v1/orders/test/status", nil)
			r.URL.RawQuery = query
			_, err := CanonicalRequest(r, "1800000000", strings.Repeat("a", 32), nil)
			require.Error(t, err)
		})
	}
}

package httpx

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicIngressRequiresBoundedExplicitShareAllowlist(t *testing.T) {
	for _, value := range []string{
		`{}`, `{"routes":[]}`, `{"routes":[],"extra":true}`,
		`{"routes":[{"method":"GET","path":"^/healthz$"}]}`,
		`{"routes":[{"method":"OPTIONS","path":"^/api/v1/share/.*$"}]}`,
		`{"routes":[{"method":"GET","path":"^/api/v1/share/["}]}`,
		`{"routes":[{"method":"GET","path":"^/api/v1/share/x$"}]} {}`,
		strings.Repeat(" ", 16<<10) + `{"routes":[{"method":"GET","path":"^/api/v1/share/x$"}]}`,
	} {
		_, err := PublicIngress(http.NotFoundHandler(), strings.NewReader(value))
		require.Error(t, err)
	}
}

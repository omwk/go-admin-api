package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func readShareOpenAPI(t *testing.T) contractMap {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi", "share.yaml"))
	require.NoError(t, err)
	var doc contractMap
	require.NoError(t, yaml.Unmarshal(data, &doc))
	return doc
}

func TestPriceListOpenAPIManifestAndPrivateSurface(t *testing.T) {
	doc := readShareOpenAPI(t)
	want, got := map[string]string{}, map[string]string{}
	for _, r := range share.Routes() {
		want[r.Method+" /api/v1/admin"+strings.ReplaceAll(r.Path, ":id", "{id}")] = r.Permission
	}
	for path, raw := range doc["paths"].(contractMap) {
		require.True(t, strings.HasPrefix(path, "/api/v1/admin/"), "M6a has no anonymous routes")
		for method, value := range raw.(contractMap) {
			if method == "parameters" {
				continue
			}
			op := value.(contractMap)
			code := op["x-permission"].(string)
			require.NoError(t, permission.Registered(code))
			got[strings.ToUpper(method)+" "+path] = code
		}
	}
	require.Equal(t, want, got)
}

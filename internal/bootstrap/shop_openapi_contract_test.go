package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func readShopOpenAPI(t *testing.T) contractMap {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi", "shop.yaml"))
	require.NoError(t, err)
	var doc contractMap
	require.NoError(t, yaml.Unmarshal(data, &doc))
	return doc
}

func TestShopOpenAPIRoutesAndPermissionsMatchManifest(t *testing.T) {
	doc := readShopOpenAPI(t)
	manifest := map[string]string{}
	for _, route := range shop.Routes() {
		manifest[route.Method+" /api/v1/admin"+routePath(route.Path)] = route.Permission
	}
	for _, route := range shop.FileRoutes() {
		manifest[route.Method+" /api/v1/admin"+routePath(route.Path)] = route.Permission
	}
	operations := map[string]string{}
	for path, raw := range doc["paths"].(contractMap) {
		for method, value := range raw.(contractMap) {
			if method == "parameters" {
				continue
			}
			operation := value.(contractMap)
			permissionCode, ok := operation["x-permission"].(string)
			require.True(t, ok, "%s %s must declare x-permission", strings.ToUpper(method), path)
			if permissionCode != "" {
				require.NoError(t, permission.Registered(permissionCode))
			}
			operations[strings.ToUpper(method)+" "+path] = permissionCode
		}
	}
	require.Equal(t, manifest, operations)
}

func routePath(path string) string {
	return strings.NewReplacer(":id", "{id}", ":cid", "{cid}").Replace(path)
}

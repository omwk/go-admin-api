package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func readCatalogOpenAPI(t *testing.T) contractMap {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi", "catalog.yaml"))
	require.NoError(t, err)
	var doc contractMap
	require.NoError(t, yaml.Unmarshal(data, &doc))
	return doc
}

func TestCatalogCategoryOpenAPIRoutesMatchManifest(t *testing.T) {
	doc := readCatalogOpenAPI(t)
	manifest, operations := map[string]string{}, map[string]string{}
	for _, route := range append(catalog.CategoryRoutes(), catalog.InventoryRoutes()...) {
		key := route.Method + " /api/v1/admin" + strings.ReplaceAll(route.Path, ":id", "{id}")
		manifest[key] = route.Permission
	}
	for _, route := range catalog.ProductRoutes() {
		key := route.Method + " /api/v1/admin" + strings.ReplaceAll(route.Path, ":id", "{id}")
		manifest[key] = route.Permission
	}
	for path, raw := range doc["paths"].(contractMap) {
		for method, value := range raw.(contractMap) {
			if method == "parameters" {
				continue
			}
			operation := value.(contractMap)
			code := operation["x-permission"].(string)
			require.NoError(t, permission.Registered(code))
			operations[strings.ToUpper(method)+" "+path] = code
		}
	}
	require.Equal(t, manifest, operations)
}

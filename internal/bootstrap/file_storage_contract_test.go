package bootstrap

import (
	"os"
	"strings"
	"testing"

	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func readStorageContract(t *testing.T, name string) contractMap {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi/" + name + ".yaml")
	require.NoError(t, err)
	var doc contractMap
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	return doc
}

func TestFileStorageOpenAPIBidirectionalContract(t *testing.T) {
	manifests := map[string]map[string]string{"storage": {}, "file": {}}
	for _, route := range system.StorageRoutes() {
		manifests["storage"][route.Method+" /api/v1"+routePath(route.Path)] = route.Permission
	}
	for _, route := range filemodule.Routes() {
		manifests["file"][route.Method+" /api/v1/admin"+routePath(route.Path)] = route.Permission
	}
	for name, manifest := range manifests {
		doc := readStorageContract(t, name)
		operations := map[string]string{}
		for path, raw := range doc["paths"].(contractMap) {
			for method, value := range raw.(contractMap) {
				if method == "parameters" {
					continue
				}
				op := value.(contractMap)
				code, ok := op["x-permission"].(string)
				require.True(t, ok)
				require.NoError(t, permission.Registered(code))
				require.NotEmpty(t, op["security"])
				require.NotEmpty(t, op["responses"])
				operations[strings.ToUpper(method)+" "+path] = code
			}
		}
		require.Equal(t, manifest, operations)
	}
}

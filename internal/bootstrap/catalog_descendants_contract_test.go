package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalogDescendantsOpenAPI(t *testing.T) {
	// BE-5 §4.7: declare the boolean, its default and the ignored-without-ID rule.
	doc := readCatalogOpenAPI(t)
	paths := doc["paths"].(contractMap)
	for _, path := range []string{"/api/v1/admin/products", "/api/v1/admin/listings"} {
		get := paths[path].(contractMap)["get"].(contractMap)
		found := false
		for _, value := range get["parameters"].([]any) {
			parameter := value.(contractMap)
			if parameter["name"] != "include_descendants" {
				continue
			}
			found = true
			require.Equal(t, "query", parameter["in"])
			schema := parameter["schema"].(contractMap)
			require.Equal(t, "boolean", schema["type"])
			require.Equal(t, false, schema["default"])
			require.Contains(t, parameter["description"], "category_id")
		}
		require.True(t, found, path)
		require.Contains(t, get["responses"], "400")
	}
	post := paths["/api/v1/admin/products/export"].(contractMap)["post"].(contractMap)
	body := post["requestBody"].(contractMap)["content"].(contractMap)["application/json"].(contractMap)["schema"].(contractMap)
	filters := body["properties"].(contractMap)["filters"].(contractMap)
	flag := filters["properties"].(contractMap)["include_descendants"].(contractMap)
	require.Equal(t, "boolean", flag["type"])
	require.Equal(t, false, flag["default"])
}

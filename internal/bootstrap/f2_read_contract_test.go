package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func f2ReadContract(t *testing.T, filename string) contractMap {
	t.Helper()
	files := contractMap{}
	for _, name := range []string{"identity.yaml", "sync.yaml", "catalog.yaml", "order.yaml", "shop.yaml"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi", name))
		require.NoError(t, err)
		var doc contractMap
		require.NoError(t, yaml.Unmarshal(raw, &doc))
		f2BindReferences(doc, name)
		files[name] = doc
	}
	doc := files[filename].(contractMap)
	doc["files"] = files
	return doc
}

func f2BindReferences(value any, filename string) {
	switch node := value.(type) {
	case map[string]any:
		for key, value := range node {
			if ref, ok := value.(string); key == "$ref" && ok {
				file, path, external := strings.Cut(ref, "#/")
				if external {
					if file == "" {
						file = filename
					}
					node[key] = "#/files/" + strings.TrimPrefix(file, "./") + "/" + path
				}
			} else {
				f2BindReferences(value, filename)
			}
		}
	case []any:
		for _, item := range node {
			f2BindReferences(item, filename)
		}
	}
}

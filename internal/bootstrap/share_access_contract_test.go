package bootstrap

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func readShareAccessOpenAPI(t *testing.T) contractMap {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi", "share-access.yaml"))
	require.NoError(t, err)
	var doc contractMap
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	return doc
}

func TestShareOpenAPIRoutesPermissionsAndAnonymousSurface(t *testing.T) {
	doc := readShareAccessOpenAPI(t)
	router := gin.New()
	require.NoError(t, share.RegisterLinkRoutes(router, &identity.AuthService{}, &identity.ProfileService{}, nil))
	share.RegisterPublicRoutes(router, nil, 1)
	actual, operations, anonymous := map[string]bool{}, map[string]bool{}, map[string]bool{}
	replace := strings.NewReplacer(":id", "{id}", ":token", "{token}", ":spu_id", "{spu_id}")
	for _, r := range router.Routes() {
		actual[r.Method+" "+replace.Replace(r.Path)] = true
	}
	wantCodes, gotCodes := map[string]string{}, map[string]string{}
	for _, r := range share.LinkRoutes() {
		wantCodes[r.Method+" /api/v1/admin"+replace.Replace(r.Path)] = r.Permission
	}
	for path, item := range doc["paths"].(contractMap) {
		for method, raw := range item.(contractMap) {
			if method == "parameters" {
				continue
			}
			key := strings.ToUpper(method) + " " + path
			operations[key] = true
			op := raw.(contractMap)
			if strings.HasPrefix(path, "/api/v1/admin/") {
				code := op["x-permission"].(string)
				require.NoError(t, permission.Registered(code))
				require.NotEmpty(t, op["security"])
				gotCodes[key] = code
			} else {
				anonymous[key] = true
				require.Empty(t, op["security"])
			}
		}
	}
	require.Equal(t, actual, operations)
	require.Equal(t, wantCodes, gotCodes)
	require.Equal(t, map[string]bool{"GET /api/v1/share/{token}": true, "POST /api/v1/share/{token}/verify": true,
		"GET /api/v1/share/{token}/items/{spu_id}": true, "POST /api/v1/share/{token}/inquiry": true}, anonymous)
}

func TestShareOpenAPIPublicDTOFieldsAreExactlyWhitelisted(t *testing.T) {
	schemas := readShareAccessOpenAPI(t)["components"].(contractMap)["schemas"].(contractMap)
	for _, tc := range []struct {
		name   string
		typeOf reflect.Type
	}{{"Content", reflect.TypeOf(share.PublicContent{})}, {"PublicItem", reflect.TypeOf(share.PublicItem{})}} {
		schema := schemas[tc.name].(contractMap)
		require.Equal(t, false, schema["additionalProperties"])
		properties := schema["properties"].(contractMap)
		want := []string{}
		for i := range tc.typeOf.NumField() {
			want = append(want, strings.Split(tc.typeOf.Field(i).Tag.Get("json"), ",")[0])
		}
		require.ElementsMatch(t, want, shareObjectKeys(properties))
	}
}

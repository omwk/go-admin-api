package bootstrap

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestSystemOpenAPIRoutesAndPermissionsMatch(t *testing.T) {
	// S2 §5.14: compare the actual router, manifest and OpenAPI in both directions.
	doc := readSystemOpenAPI(t)
	require.Equal(t, "3.0.3", doc["openapi"])
	router := gin.New()
	require.NoError(t, system.RegisterRoutes(router, &identity.AuthService{}, &identity.ProfileService{}, &system.SettingService{}, &system.DictService{}, systemWriter(&identity.AdminService{})))
	replace := strings.NewReplacer(":group", "{group}", ":id", "{id}", ":type", "{type}")
	actual, manifest, documented := map[string]bool{}, map[string]string{}, map[string]string{}
	for _, route := range router.Routes() {
		actual[route.Method+" "+replace.Replace(route.Path)] = true
	}
	for _, route := range system.Routes() {
		key := route.Method + " /api/v1" + replace.Replace(route.Path)
		require.NotContains(t, manifest, key)
		if route.Permission != "" {
			require.NoError(t, permission.Registered(route.Permission))
		}
		manifest[key] = route.Permission
	}
	for path, raw := range doc["paths"].(contractMap) {
		for method, definition := range raw.(contractMap) {
			if method == "parameters" {
				continue
			}
			operation := definition.(contractMap)
			require.NotEmpty(t, operation["security"])
			code, _ := operation["x-permission"].(string)
			documented[strings.ToUpper(method)+" "+path] = code
		}
	}
	require.Len(t, actual, 13)
	require.Len(t, actual, len(manifest))
	for key := range actual {
		require.Contains(t, manifest, key)
	}
	require.Equal(t, manifest, documented)
}

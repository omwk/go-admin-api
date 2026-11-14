package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
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
	require.NoError(t, registerCredentialNotificationRoutes(router, &identity.AuthService{}, &identity.ProfileService{}, &credentialRefreshRuntime{}))
	require.NoError(t, system.RegisterAuditRoutes(router, &identity.AuthService{}, &identity.ProfileService{}, &system.AuditService{}))
	require.NoError(t, identity.RegisterTenantRoutes(router, &identity.AuthService{}, &identity.ProfileService{}, &identity.TenantService{}))
	replace := strings.NewReplacer(":group", "{group}", ":id", "{id}", ":type", "{type}", ":code", "{code}")
	actual, manifest, documented := map[string]bool{}, map[string]string{}, map[string]string{}
	for _, route := range router.Routes() {
		actual[route.Method+" "+replace.Replace(route.Path)] = true
	}
	for _, route := range append(system.Routes(), system.AuditRoutes()...) {
		key := route.Method + " /api/v1" + replace.Replace(route.Path)
		require.NotContains(t, manifest, key)
		if route.Permission != "" {
			require.NoError(t, permission.Registered(route.Permission))
		}
		manifest[key] = route.Permission
	}
	for route, code := range s3aNotificationRoutes() {
		require.NotContains(t, manifest, route)
		if code != "" {
			require.NoError(t, permission.Registered(code))
		}
		manifest[route] = code
	}
	addSystemUnlockContract(t, replace, actual, manifest)
	for _, route := range identity.TenantRoutes() {
		require.NoError(t, permission.Registered(route.Permission))
		key := route.Method + " /api/v1/admin" + replace.Replace(route.Path)
		require.NotContains(t, manifest, key)
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
	require.Len(t, actual, 46)
	require.Len(t, actual, len(manifest))
	for key := range actual {
		require.Contains(t, manifest, key)
	}
	require.Equal(t, manifest, documented)
	t.Logf("actual=%d manifest=%d OpenAPI=%d; tenant operations=13; permissions match in both directions", len(actual), len(manifest), len(documented))
}

func addSystemUnlockContract(t *testing.T, replace *strings.Replacer, actual map[string]bool, manifest map[string]string) {
	t.Helper()
	// Unlock is owned by identity and is also published in the system-layer contract.
	router := gin.New()
	require.NoError(t, identity.RegisterAdminRoutes(router, &identity.AuthService{}, &identity.ProfileService{}, &identity.AdminService{}))
	for _, route := range router.Routes() {
		if route.Path == "/api/v1/admin/users/:id/unlock" {
			actual[route.Method+" "+replace.Replace(route.Path)] = true
		}
	}
	for _, route := range identity.AdminRoutes() {
		if route.Path == "/users/:id/unlock" {
			require.NoError(t, permission.Registered(route.Permission))
			manifest[route.Method+" /api/v1/admin"+replace.Replace(route.Path)] = route.Permission
		}
	}
}

func TestSystemSyncSettingsOpenAPI(t *testing.T) {
	// BE-8 §4.9: both group operations document sync and share an accurate response example.
	doc := readSystemOpenAPI(t)
	components := doc["components"].(contractMap)
	require.Contains(t, components, "examples")
	examples := components["examples"].(contractMap)
	require.Contains(t, examples, "SyncSettings")
	example := examples["SyncSettings"].(contractMap)["value"]
	raw, err := json.Marshal(example)
	require.NoError(t, err)
	var envelope struct{ Data system.SettingGroup }
	require.NoError(t, json.Unmarshal(raw, &envelope))
	items, ok := setting.DefaultRegistry().Items("sync")
	require.True(t, ok)
	expected := system.SettingGroup{Group: "sync", Items: []system.SettingValue{}}
	for _, item := range items {
		expected.Items = append(expected.Items, system.SettingValue{Schema: item, Value: item.Default})
	}
	want, err := json.Marshal(expected)
	require.NoError(t, err)
	got, err := json.Marshal(envelope.Data)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
	path := "/api/v1/admin/settings/{group}"
	operations := doc["paths"].(contractMap)[path].(contractMap)
	for _, method := range []string{"get", "put"} {
		operation := operations[method].(contractMap)
		parameter := operation["parameters"].([]any)[0].(contractMap)
		require.ElementsMatch(t, []any{"security", "system", "notification", "sync", "tenant"}, parameter["schema"].(contractMap)["enum"])
		response := operation["responses"].(contractMap)["200"].(contractMap)
		media := response["content"].(contractMap)["application/json"].(contractMap)
		require.Equal(t, "#/components/examples/SyncSettings", media["examples"].(contractMap)["sync"].(contractMap)["$ref"])
		validateOpenAPIResponse(t, doc, path, method, 200, raw)
	}
	schemas := components["schemas"].(contractMap)
	properties := schemas["SettingSchema"].(contractMap)["properties"].(contractMap)
	require.ElementsMatch(t, []any{"security", "system", "notification", "sync", "tenant"}, properties["group"].(contractMap)["enum"])
	t.Log("GET/PUT sync response example matches all three registered schemas/defaults and passes OpenAPI validation")
}

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

func TestAdminRouteOpenAPIAndPermissionsAreExactlyEqual(t *testing.T) {
	// REQ-IAM-004/006: the actual runtime routes, manifest and contract must agree in both directions.
	doc := readIdentityOpenAPI(t)
	router := gin.New()
	auth, profiles := &identity.AuthService{}, &identity.ProfileService{}
	identity.RegisterAuthRoutes(router, auth, "default")
	identity.RegisterProfileRoutes(router, auth, profiles)
	identity.RegisterAccountSelfRoutes(router, auth, &identity.AccountService{})
	identity.RegisterAccountRegistrationRoutes(router, &identity.AccountService{})
	require.NoError(t, identity.RegisterAdminRoutes(router, auth, profiles, &identity.AdminService{}))
	actual := map[string]bool{}
	for _, route := range router.Routes() {
		actual[route.Method+" "+strings.NewReplacer(":id", "{id}", ":sid", "{sid}").Replace(route.Path)] = true
	}
	manifest := map[string]string{}
	for _, route := range identity.AdminRoutes() {
		key := route.Method + " /api/v1/admin" + strings.NewReplacer(":id", "{id}", ":sid", "{sid}").Replace(route.Path)
		require.NotContains(t, manifest, key)
		require.NoError(t, permission.Registered(route.Permission))
		manifest[key] = route.Permission
	}
	addTenantSettingsContract(t, actual, manifest)
	operations := map[string]bool{}
	adminCodes := map[string]string{}
	for path, item := range doc["paths"].(contractMap) {
		for method, raw := range item.(contractMap) {
			if method == "parameters" {
				continue
			}
			require.Contains(t, []string{"get", "post", "put", "delete"}, method)
			key := strings.ToUpper(method) + " " + path
			operations[key] = true
			operation := raw.(contractMap)
			if strings.HasPrefix(path, "/api/v1/admin/") {
				code, ok := operation["x-permission"].(string)
				require.True(t, ok)
				require.NoError(t, permission.Registered(code))
				adminCodes[key] = code
				require.NotEmpty(t, operation["security"])
				if path == "/api/v1/admin/settings/tenant" {
					require.Equal(t, []any{permission.SystemTenantManage, permission.SystemSettingUpdate}, operation["x-permissions-any"])
				}
			}
		}
	}
	require.Len(t, manifest, 35)
	require.Len(t, actual, 48)
	// These existing self-service operations are outside BE-IDN-003's contract scope.
	for _, key := range []string{"PUT /api/v1/auth/password", "DELETE /api/v1/auth/sessions/{sid}", "POST /api/v1/auth/logout-all"} {
		require.Contains(t, actual, key)
		require.NotContains(t, operations, key)
		delete(actual, key)
	}
	require.Len(t, actual, 45)
	require.Equal(t, actual, operations)
	require.Equal(t, manifest, adminCodes)
}

func addTenantSettingsContract(t *testing.T, actual map[string]bool, manifest map[string]string) {
	t.Helper()
	router := gin.New()
	require.NoError(t, system.RegisterRoutes(router, &identity.AuthService{}, &identity.ProfileService{}, &system.SettingService{}, &system.DictService{}, systemWriter(&identity.AdminService{})))
	count := 0
	for _, route := range router.Routes() {
		if route.Path == "/api/v1/admin/settings/:group" {
			key := route.Method + " /api/v1/admin/settings/tenant"
			require.NotContains(t, actual, key)
			actual[key] = true
			manifest[key] = permission.SystemSettingUpdate
			count++
		}
	}
	require.Equal(t, 2, count)
}

func TestAdminRegistrationRejectsUnknownAndAbbreviatedCodes(t *testing.T) {
	for _, code := range []string{"system:unknown:action", "system:user", "system:user:list/create"} {
		guard, err := identity.RequirePerm(&identity.ProfileService{}, code)
		require.Error(t, err)
		require.Nil(t, guard)
	}
}

func TestAdminUserFilterQueryParametersAreDocumented(t *testing.T) {
	// BE-1: listUsers declares optional filters with matching validation limits.
	doc := readIdentityOpenAPI(t)
	operation := doc["paths"].(contractMap)["/api/v1/admin/users"].(contractMap)["get"].(contractMap)
	parameters := map[string]contractMap{}
	for _, raw := range operation["parameters"].([]any) {
		parameter := resolveContract(t, doc, raw.(contractMap))
		parameters[parameter["name"].(string)] = parameter
	}
	for _, name := range []string{"keyword", "status"} {
		require.Contains(t, parameters, name)
		require.Equal(t, "query", parameters[name]["in"])
		require.NotEqual(t, true, parameters[name]["required"])
		require.Equal(t, true, parameters[name]["allowEmptyValue"])
		require.Equal(t, "string", parameters[name]["schema"].(contractMap)["type"])
	}
	require.EqualValues(t, 256, parameters["keyword"]["schema"].(contractMap)["maxLength"])
	require.Equal(t, []any{"active", "disabled", "locked"}, parameters["status"]["schema"].(contractMap)["enum"])
}

func TestAdminDeletionConflictsAreDocumented(t *testing.T) {
	doc := readIdentityOpenAPI(t)
	for _, resource := range []string{"roles", "menus"} {
		path := doc["paths"].(contractMap)["/api/v1/admin/"+resource+"/{id}"].(contractMap)
		operation := path["delete"].(contractMap)
		response := resolveContract(t, doc, operation["responses"].(contractMap)["409"].(contractMap))
		description := response["description"].(string)
		if resource == "roles" {
			for _, table := range []string{"sys_user_role", "sys_role_menu", "sys_role_permission", "sys_role_dept"} {
				require.Contains(t, description, table)
			}
			require.Contains(t, description, "20105")
			require.Contains(t, description, "super_admin")
		} else {
			require.Contains(t, description, "details.children")
			require.Contains(t, description, "details.roles")
			require.Contains(t, description, "10004")
		}
	}
}

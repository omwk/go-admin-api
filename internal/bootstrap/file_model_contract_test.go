package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func TestFileModelAvatarOpenAPIBidirectional(t *testing.T) {
	router := gin.New()
	require.NoError(t, filemodule.RegisterAvatarRoutes(router, &identity.AuthService{}, &identity.ProfileService{}, &filemodule.Service{}, filemodule.AvatarProfiles{Save: func(context.Context, int64, int64, string) error { return nil }}))
	actual, manifest, documented := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, route := range router.Routes() {
		actual[route.Method+" "+strings.ReplaceAll(route.Path, ":user_id", "{user_id}")] = true
	}
	for _, route := range filemodule.AvatarRoutes() {
		manifest[route.Method+" "+strings.ReplaceAll(route.Path, ":user_id", "{user_id}")] = true
	}
	doc := readStorageContract(t, "avatar")
	for path, raw := range doc["paths"].(contractMap) {
		for method, value := range raw.(contractMap) {
			documented[strings.ToUpper(method)+" "+path] = true
			op := value.(contractMap)
			require.Contains(t, op, "security")
			require.NotEmpty(t, op["responses"])
			if method == "post" {
				require.NotEmpty(t, op["security"])
			} else {
				require.Empty(t, op["security"])
			}
		}
	}
	require.Equal(t, actual, manifest)
	require.Equal(t, manifest, documented)
}

func TestFileModelListAndProviderContractFields(t *testing.T) {
	doc := readStorageContract(t, "file")
	file := doc["components"].(contractMap)["schemas"].(contractMap)["File"].(contractMap)
	properties := file["properties"].(contractMap)
	for _, key := range []string{"source", "category", "thumb_url", "thumb_status", "uploaded_by_name"} {
		require.Contains(t, properties, key)
		require.Contains(t, file["required"], key)
	}
	require.NotContains(t, properties, "purpose")
	operation := doc["paths"].(contractMap)["/api/v1/admin/files"].(contractMap)["get"].(contractMap)
	parameters := map[string]bool{}
	for _, raw := range operation["parameters"].([]any) {
		parameters[raw.(contractMap)["name"].(string)] = true
	}
	require.Contains(t, parameters, "category")
	require.Contains(t, parameters, "include_avatar")
	require.NotContains(t, parameters, "purpose")
	provider := readStorageContract(t, "storage")["components"].(contractMap)["schemas"].(contractMap)
	for _, name := range []string{"ProviderInput", "ProviderFields"} {
		fields := provider[name].(contractMap)["properties"].(contractMap)
		require.Contains(t, fields, "access_mode")
		require.Contains(t, fields, "quota_per_user_mb")
	}
}

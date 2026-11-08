package bootstrap

import (
	"os"
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

func TestShareInquiryOpenAPIExactRoutesAndPermissions(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi/share-inquiry.yaml")
	require.NoError(t, err)
	var doc contractMap
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	router := gin.New()
	share.RegisterInquiryRoutes(router, &identity.AuthService{}, nil)
	actual, expected := map[string]string{}, map[string]string{}
	for _, route := range share.InquiryRoutes() {
		key := route.Method + " /api/v1/admin" + strings.ReplaceAll(route.Path, ":id", "{id}")
		expected[key] = route.Permission
	}
	for path, item := range doc["paths"].(contractMap) {
		for method, raw := range item.(contractMap) {
			if method == "parameters" {
				continue
			}
			op := raw.(contractMap)
			require.NotEmpty(t, op["security"])
			code := op["x-permission"].(string)
			require.NoError(t, permission.Registered(code))
			actual[strings.ToUpper(method)+" "+path] = code
		}
	}
	require.Equal(t, expected, actual)
	require.Len(t, router.Routes(), len(expected))
	schemas := doc["components"].(contractMap)["schemas"].(contractMap)
	for name, kind := range map[string]reflect.Type{"MaskedInquiry": reflect.TypeOf(share.InquiryView{}), "Visit": reflect.TypeOf(share.Visit{}), "Submission": reflect.TypeOf(share.InquiryInput{}), "InquiryItem": reflect.TypeOf(share.InquiryItem{})} {
		schema := schemas[name].(contractMap)
		require.Equal(t, false, schema["additionalProperties"])
		fields := []string{}
		for i := range kind.NumField() {
			fields = append(fields, strings.Split(kind.Field(i).Tag.Get("json"), ",")[0])
		}
		require.ElementsMatch(t, fields, shareObjectKeys(schema["properties"].(contractMap)), name)
	}
}

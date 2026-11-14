package bootstrap

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

func TestProfileFieldsOpenAPIContract(t *testing.T) {
	// BE-IDN-003 §4.12: every JSON field is documented, and every documented field is implemented.
	doc := readIdentityOpenAPI(t)
	schemas := doc["components"].(contractMap)["schemas"].(contractMap)
	for _, tc := range []struct {
		name string
		kind reflect.Type
	}{
		{"User", reflect.TypeOf(identity.UserView{})},
		{"ProfileRequest", reflect.TypeOf(identity.ProfileInput{})},
		{"OwnSession", reflect.TypeOf(repo.OwnSession{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema, ok := schemas[tc.name].(contractMap)
			require.True(t, ok)
			properties := schema["properties"].(contractMap)
			implemented := []string{}
			for i := 0; i < tc.kind.NumField(); i++ {
				name := strings.Split(tc.kind.Field(i).Tag.Get("json"), ",")[0]
				implemented = append(implemented, name)
			}
			documented := []string{}
			for name := range properties {
				documented = append(documented, name)
			}
			require.ElementsMatch(t, implemented, documented)
		})
	}
	for _, pair := range [][2]string{{"User", "email"}, {"User", "phone"}, {"User", "dept_name"}, {"OwnSession", "ip"}, {"OwnSession", "user_agent"}} {
		schema, ok := schemas[pair[0]].(contractMap)
		require.True(t, ok)
		require.Equal(t, true, schema["properties"].(contractMap)[pair[1]].(contractMap)["nullable"])
		require.Contains(t, schema["required"], pair[1])
	}
	require.Equal(t, false, schemas["OwnSession"].(contractMap)["additionalProperties"])
}

func TestProfileFieldsOpenAPIIntegrationResponses(t *testing.T) {
	f := profileFieldsFixture(t)
	doc := readIdentityOpenAPI(t)
	phone := profilePhone(t)
	input := map[string]any{"nickname": "profile-contract", "phone": phone}
	schema := doc["components"].(contractMap)["schemas"].(contractMap)["ProfileRequest"].(contractMap)
	validateContract(t, doc, schema, input)
	response := f.request(t, "PUT", "/auth/profile", f.token, input)
	require.Equal(t, 200, response.Code)
	validateOpenAPIResponse(t, doc, "/api/v1/auth/profile", "put", response.Code, response.Body.Bytes())
	for _, path := range []string{"/auth/me", "/auth/sessions"} {
		response = f.request(t, "GET", path, f.token, nil)
		require.Equal(t, 200, response.Code)
		validateOpenAPIResponse(t, doc, "/api/v1"+path, "get", response.Code, response.Body.Bytes())
	}
	response = f.request(t, "PUT", "/auth/profile", f.token, map[string]any{"phone": "12345"})
	require.Equal(t, 400, response.Code)
	validateOpenAPIResponse(t, doc, "/api/v1/auth/profile", "put", response.Code, response.Body.Bytes())
	t.Log("profile input plus profile/me/own-session JSON responses match OpenAPI in both directions")
}

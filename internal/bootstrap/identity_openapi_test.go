package bootstrap

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type contractMap = map[string]any

func readIdentityOpenAPI(t *testing.T) contractMap {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi", "identity.yaml"))
	require.NoError(t, err)
	var doc contractMap
	require.NoError(t, yaml.Unmarshal(data, &doc))
	return doc
}

func resolveContract(t *testing.T, doc, object contractMap) contractMap {
	t.Helper()
	ref, ok := object["$ref"].(string)
	if !ok {
		return object
	}
	require.True(t, strings.HasPrefix(ref, "#/"))
	var node any = doc
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		node = node.(contractMap)[part]
	}
	resolved, ok := node.(contractMap)
	require.True(t, ok, "OpenAPI local reference must exist")
	return resolved
}

func validateContract(t *testing.T, doc, schema contractMap, value any) {
	t.Helper()
	schema = resolveContract(t, doc, schema)
	if value == nil {
		require.Equal(t, true, schema["nullable"], "null requires a nullable schema")
		return
	}
	if all, ok := schema["allOf"].([]any); ok {
		for _, part := range all {
			validateContract(t, doc, part.(contractMap), value)
		}
	}
	validateContractEnum(t, schema, value)
	switch schema["type"] {
	case "object":
		validateContractObject(t, doc, schema, value)
	case "array":
		items, ok := value.([]any)
		require.True(t, ok, "schema requires an array")
		for _, item := range items {
			validateContract(t, doc, schema["items"].(contractMap), item)
		}
	case "string":
		_, ok := value.(string)
		require.True(t, ok, "schema requires a string")
	case "boolean":
		_, ok := value.(bool)
		require.True(t, ok, "schema requires a boolean")
	case "integer":
		number, ok := value.(float64)
		require.True(t, ok, "JSON schema requires an integer")
		require.Equal(t, number, float64(int64(number)))
	}
}

func validateContractEnum(t *testing.T, schema contractMap, value any) {
	t.Helper()
	choices, ok := schema["enum"].([]any)
	if !ok {
		return
	}
	found := false
	actual, err := json.Marshal(value)
	require.NoError(t, err)
	for _, choice := range choices {
		encoded, err := json.Marshal(choice)
		require.NoError(t, err)
		if string(encoded) == string(actual) {
			found = true
		}
	}
	require.True(t, found, "response value must match schema enum")
}

func validateContractObject(t *testing.T, doc, schema contractMap, value any) {
	t.Helper()
	object, ok := value.(contractMap)
	require.True(t, ok, "schema requires an object")
	if required, ok := schema["required"].([]any); ok {
		for _, key := range required {
			require.Contains(t, object, key.(string))
		}
	}
	properties, _ := schema["properties"].(contractMap)
	for key, field := range object {
		if nested, ok := properties[key].(contractMap); ok {
			validateContract(t, doc, nested, field)
		} else if nested, ok := schema["additionalProperties"].(contractMap); ok {
			validateContract(t, doc, nested, field)
		} else if schema["additionalProperties"] == false {
			t.Fatalf("unexpected schema property %s", key)
		}
	}
}

func validateOpenAPIResponse(t *testing.T, doc contractMap, path, method string, status int, body []byte) {
	t.Helper()
	operation := doc["paths"].(contractMap)[path].(contractMap)[method].(contractMap)
	response, ok := operation["responses"].(contractMap)[strconv.Itoa(status)].(contractMap)
	require.True(t, ok, "HTTP status must be documented")
	response = resolveContract(t, doc, response)
	if status == 204 {
		require.Empty(t, body)
		return
	}
	schema := response["content"].(contractMap)["application/json"].(contractMap)["schema"].(contractMap)
	var value any
	require.NoError(t, json.Unmarshal(body, &value))
	validateContract(t, doc, schema, value)
}

func TestIdentityOpenAPIContainsExactlySixAuthRoutes(t *testing.T) {
	doc := readIdentityOpenAPI(t)
	require.Equal(t, "3.0.3", doc["openapi"])
	paths := doc["paths"].(contractMap)
	authPaths := map[string]any{}
	for path, operation := range paths {
		if strings.HasPrefix(path, "/api/v1/auth/") {
			authPaths[path] = operation
		}
	}
	require.Len(t, authPaths, 6)
	for path, method := range map[string]string{"/api/v1/auth/login": "post", "/api/v1/auth/refresh": "post", "/api/v1/auth/logout": "post", "/api/v1/auth/reauth": "post", "/api/v1/auth/me": "get", "/api/v1/auth/menus": "get"} {
		require.Contains(t, paths, path)
		operation := paths[path].(contractMap)[method].(contractMap)
		if path != "/api/v1/auth/login" && path != "/api/v1/auth/refresh" {
			require.NotEmpty(t, operation["security"])
		}
	}
}

func TestIdentityOpenAPIIntegrationMatchesAllSixResponses(t *testing.T) {
	f, router := seededProfileFixture(t)
	doc := readIdentityOpenAPI(t)
	response, login := authRequest(t, router, "/api/v1/auth/login", map[string]string{"username": identity.SeedAdminUsername, "password": f.password}, "")
	require.Equal(t, 200, response.Code)
	validateOpenAPIResponse(t, doc, "/api/v1/auth/login", "post", response.Code, response.Body.Bytes())
	response, _ = authRequest(t, router, "/api/v1/auth/reauth", map[string]string{"password": f.password}, login.Data.AccessToken)
	require.Equal(t, 200, response.Code)
	validateOpenAPIResponse(t, doc, "/api/v1/auth/reauth", "post", response.Code, response.Body.Bytes())
	response, rotated := authRequest(t, router, "/api/v1/auth/refresh", map[string]string{"refresh_token": login.Data.RefreshToken}, "")
	require.Equal(t, 200, response.Code)
	validateOpenAPIResponse(t, doc, "/api/v1/auth/refresh", "post", response.Code, response.Body.Bytes())
	response, _ = authRequest(t, router, "/api/v1/auth/refresh", map[string]string{"refresh_token": login.Data.RefreshToken}, "")
	validateOpenAPIResponse(t, doc, "/api/v1/auth/refresh", "post", response.Code, response.Body.Bytes())
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/auth/menus"} {
		response, _ = profileRequest(t, router, path, rotated.Data.AccessToken)
		require.Equal(t, http.StatusOK, response.Code)
		validateOpenAPIResponse(t, doc, path, "get", response.Code, response.Body.Bytes())
		response, _ = profileRequest(t, router, path, "")
		validateOpenAPIResponse(t, doc, path, "get", response.Code, response.Body.Bytes())
	}
	response, _ = authRequest(t, router, "/api/v1/auth/logout", nil, rotated.Data.AccessToken)
	require.Equal(t, 204, response.Code)
	validateOpenAPIResponse(t, doc, "/api/v1/auth/logout", "post", response.Code, response.Body.Bytes())
}

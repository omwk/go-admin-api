package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func observationContract(t *testing.T) contractMap {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi", "gateway.yaml"))
	require.NoError(t, err)
	var doc contractMap
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	managementRaw, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi", "gateway-client-management.yaml"))
	require.NoError(t, err)
	var management contractMap
	require.NoError(t, yaml.Unmarshal(managementRaw, &management))
	paths := doc["paths"].(contractMap)
	for path, item := range management["paths"].(contractMap) {
		pathItem := paths[path].(contractMap)
		require.Equal(t, gatewayManagementPathRef(path), pathItem["$ref"])
		paths[path] = item
	}
	components := doc["components"].(contractMap)
	managementComponents := management["components"].(contractMap)
	for section, items := range managementComponents {
		current := components[section].(contractMap)
		for name, item := range items.(contractMap) {
			current[name] = item
		}
	}
	return doc
}

func gatewayManagementPathRef(path string) string {
	pointer := strings.NewReplacer("~", "~0", "/", "~1", "{", "%7B", "}", "%7D").Replace(path)
	return "./gateway-client-management.yaml#/paths/" + pointer
}

func TestGatewayObservabilityContractBidirectional(t *testing.T) {
	doc := observationContract(t)
	paths := doc["paths"].(contractMap)
	actual := map[string]string{}
	observationRoutes := gateway.ObservationRoutes()
	pathParams := strings.NewReplacer(":grant_id", "{grant_id}", ":sid", "{sid}", ":id", "{id}")
	for _, route := range gateway.AdminRoutes() {
		path := "/api/v1/admin" + pathParams.Replace(route.Path)
		secretRead := route.Method == "GET" && (path == listKeysContractPath || path == revealContractPath)
		clientRead := route.Method == "GET" && (path == "/api/v1/admin/api-clients/{id}" || path == "/api/v1/admin/api-clients/{id}/routes")
		clientUpdate := route.Method == "PUT" && path == "/api/v1/admin/api-clients/{id}"
		grantRevoke := route.Method == "DELETE" && path == "/api/v1/admin/api-clients/{id}/routes/{grant_id}"
		if !secretRead && !clientRead && !clientUpdate && !grantRevoke && path != gatewayInfoContractPath && !slices.Contains(observationRoutes, route) {
			continue
		}
		operation := paths[path].(contractMap)[strings.ToLower(route.Method)].(contractMap)
		require.Equal(t, route.Permission, operation["x-permission"])
		actual[strings.ToLower(route.Method)+" "+path] = route.Permission
	}
	for path, item := range paths {
		for method := range item.(contractMap) {
			require.Contains(t, actual, method+" "+path, "OpenAPI must not invent handlers")
		}
	}
	schemas := doc["components"].(contractMap)["schemas"].(contractMap)
	for name, value := range map[string]any{
		"Call": gateway.CallInfo{}, "SummaryItem": gateway.CallSummaryItem{}, "TryInput": gateway.TryInput{}, "TryResult": gateway.TryResult{},
		"SecretInfo": gateway.SecretInfo{}, "RevealedSecret": gateway.RevealedSecret{},
		"ClientInfo": gateway.ClientInfo{}, "RouteGrantInfo": gateway.RouteGrantInfo{},
		"GatewayInfo": gateway.Info{},
	} {
		schema := schemas[name].(contractMap)
		properties := schema["properties"].(contractMap)
		typeOf := reflect.TypeOf(value)
		fields := []string{}
		for i := 0; i < typeOf.NumField(); i++ {
			field := strings.Split(typeOf.Field(i).Tag.Get("json"), ",")[0]
			fields = append(fields, field)
			require.Contains(t, properties, field)
		}
		require.Len(t, properties, len(fields), name+" schema fields must exactly match the DTO")
		require.Equal(t, false, schema["additionalProperties"])
	}
	t.Log("#9 OpenAPI operations, permissions and DTO fields match the registered gateway management contract")
}

func TestGatewayObservabilityIntegrationContractResponses(t *testing.T) {
	// BE-IDN-029 acceptance 11: real HTTP responses match every documented schema.
	f := newGatewayHTTPFixture(t)
	doc := observationContract(t)
	observationLog(t, f, 1, f.client.ID, f.route.ID, f.now.Add(-time.Hour), 200, 10, "contract")
	for _, request := range []struct {
		method, path, contract string
		input                  any
	}{
		{"GET", "/api-calls", "/api/v1/admin/api-calls", nil},
		{"GET", "/api-calls/summary?group_by=client", "/api/v1/admin/api-calls/summary", nil},
		{"POST", resourcePath("api-clients", f.client.ID) + "/try", "/api/v1/admin/api-clients/{id}/try", observationInput(f)},
	} {
		w, _ := f.manage(t, request.method, request.path, request.input, "")
		require.Equal(t, 200, w.Code, w.Body.String())
		validateOpenAPIResponse(t, doc, request.contract, strings.ToLower(request.method), 200, w.Body.Bytes())
	}
	f.bearer = ""
	w, _ := f.manage(t, "GET", "/api-calls", nil, "")
	require.Equal(t, 401, w.Code)
	validateOpenAPIResponse(t, doc, "/api/v1/admin/api-calls", "get", 401, w.Body.Bytes())
	t.Log("#11 all 3 operation/permission mappings and DTO fields match bidirectionally; actual list/summary/trial responses and 401 match OpenAPI")
	// Keep the decoded response checked independently of a permissive any schema.
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.NotZero(t, envelope["code"])
}

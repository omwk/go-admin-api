package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type systemFixture struct {
	*adminFixture
	settings *system.SettingService
	dicts    *system.DictService
	doc      contractMap
}

func readSystemOpenAPI(t *testing.T) contractMap {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi/system.yaml")
	require.NoError(t, err)
	var doc contractMap
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	return doc
}

func newSystemFixture(t *testing.T, registry *setting.Registry) *systemFixture {
	t.Helper()
	admin := newAdminFixture(t)
	settings, dicts, err := newSystemServices(admin.database)
	require.NoError(t, err)
	if registry != nil {
		settings, err = system.NewSettingService(&systemSettingStore{database: admin.database}, registry, func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, admin.database, event) })
		require.NoError(t, err)
	}
	profiles, err := identity.NewProfileService(newProfileStore(admin.database))
	require.NoError(t, err)
	admin.identityFixture.service.UseSettings(settings)
	admin.service.UseSettings(settings)
	require.NoError(t, system.RegisterRoutes(admin.router, admin.identityFixture.service, profiles, settings, dicts, systemWriter(admin.service)))
	require.NoError(t, registerSystemAuditRoutes(admin.router, admin.identityFixture.service, profiles, admin.database))
	return &systemFixture{adminFixture: admin, settings: settings, dicts: dicts, doc: readSystemOpenAPI(t)}
}

func systemRequest(t *testing.T, f *systemFixture, method, path string, payload any, key string) (*httptest.ResponseRecorder, adminEnvelope) {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if f.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+f.bearer)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	validateOpenAPIResponse(t, f.doc, systemContractPath(t, path), strings.ToLower(method), response.Code, response.Body.Bytes())
	var envelope adminEnvelope
	if response.Code != 204 {
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	}
	return response, envelope
}

func systemContractPath(t *testing.T, path string) string {
	t.Helper()
	actual := strings.Split(strings.Split(path, "?")[0], "/")
	for _, route := range append(system.Routes(), system.AuditRoutes()...) {
		parts := strings.Split(route.Path, "/")
		if len(parts) != len(actual) {
			continue
		}
		match := true
		for i, part := range parts {
			if strings.HasPrefix(part, ":") {
				parts[i] = "{" + part[1:] + "}"
			} else if part != actual[i] {
				match = false
			}
		}
		if match {
			return "/api/v1" + strings.Join(parts, "/")
		}
	}
	t.Fatalf("undocumented system path %s", path)
	return ""
}

func systemGroupValue(t *testing.T, data json.RawMessage, key string) any {
	t.Helper()
	var group system.SettingGroup
	require.NoError(t, json.Unmarshal(data, &group))
	for _, item := range group.Items {
		if item.Schema.Key == key {
			return item.Value
		}
	}
	t.Fatalf("setting %s missing", key)
	return nil
}

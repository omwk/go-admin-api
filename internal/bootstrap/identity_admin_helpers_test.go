package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

type adminEnvelope struct {
	Code    int             `json:"code"`
	Data    json.RawMessage `json:"data"`
	Details map[string]any  `json:"details"`
}
type adminFixture struct {
	*identityFixture
	router   *gin.Engine
	bearer   string
	actor    identity.Overview
	service  *identity.AdminService
	store    *adminStore
	contract contractMap
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	f, router := seededProfileFixture(t)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	store := newAdminStore(f.database)
	service, err := identity.NewAdminService(store, func(ctx context.Context, event audit.Event) error { return audit.Record(ctx, f.database, event) })
	require.NoError(t, err)
	service.UseInvalidator(authz.New(f.redis.cache).Pending)
	service.UseSessions(&sessionStore{admin: store}, f.service)
	require.NoError(t, identity.RegisterAdminRoutes(router, f.service, profiles, service))
	tokens, err := f.service.Login(f.ctx, 1, identity.SeedAdminUsername, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	user := seededAdminID(t, f)
	actor, err := profiles.Me(f.ctx, 1, user)
	require.NoError(t, err)
	f.ctx, err = store.Authorize(f.ctx, 1, user)
	require.NoError(t, err)
	return &adminFixture{identityFixture: f, router: router, bearer: tokens.AccessToken, actor: actor, service: service, store: store, contract: readIdentityOpenAPI(t)}
}

func seededAdminID(t *testing.T, f *identityFixture) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row model.User
	require.NoError(t, q.Select("id").Where("tenant_id = ? AND username = ?", 1, identity.SeedAdminUsername).Take(&row).Error)
	return row.ID
}

func adminRequest(t *testing.T, f *adminFixture, method, path string, payload any, key string) (*httptest.ResponseRecorder, adminEnvelope) {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, "/api/v1/admin"+path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if f.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+f.bearer)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	contractPath := strings.Split(path, "?")[0]
	segments := strings.Split(contractPath, "/")
	if len(segments) > 2 {
		if segments[1] == "sessions" {
			segments[2] = "{sid}"
		}
		if _, err := strconv.ParseInt(segments[2], 10, 64); err == nil {
			segments[2] = "{id}"
		}
	}
	validateOpenAPIResponse(t, f.contract, "/api/v1/admin"+strings.Join(segments, "/"), strings.ToLower(method), response.Code, response.Body.Bytes())
	var envelope adminEnvelope
	if response.Code != 204 {
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	}
	return response, envelope
}

func createAdminRole(t *testing.T, f *adminFixture, code string) model.Role {
	t.Helper()
	response, envelope := adminRequest(t, f, http.MethodPost, "/roles", identity.RoleRequest{Code: code, Name: code, DataScope: "custom", Status: "active"}, "")
	require.Equal(t, 201, response.Code, response.Body.String())
	var row model.Role
	require.NoError(t, json.Unmarshal(envelope.Data, &row))
	return row
}

func createAdminMenu(t *testing.T, f *adminFixture, name string, parent int64) model.Menu {
	t.Helper()
	response, envelope := adminRequest(t, f, http.MethodPost, "/menus", identity.MenuRequest{ParentID: parent, Name: name, Type: "menu", Status: "disabled"}, "")
	require.Equal(t, 201, response.Code, response.Body.String())
	var row model.Menu
	require.NoError(t, json.Unmarshal(envelope.Data, &row))
	return row
}

func resourcePath(resource string, id int64) string {
	return "/" + resource + "/" + strconv.FormatInt(id, 10)
}

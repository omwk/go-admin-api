package bootstrap

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
	"github.com/stretchr/testify/require"
)

type shopHTTPEnvelope struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
}

func TestShopCRUDIntegrationCrossOwnerIsolation(t *testing.T) {
	// REQ-SHP-002 / ADR-015 Q4: all five CRUD entries use the shop owner scope.
	f := newAdminFixture(t)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	service, err := shop.NewService(newShopStore(f.database), f.store.Authorize)
	require.NoError(t, err)
	require.NoError(t, shop.RegisterRoutes(f.router, f.identityFixture.service, profiles, service))

	other := model.User{TenantID: 1, Username: "shop-cross-owner", PasswordHash: f.user.PasswordHash, DepartmentID: f.user.DepartmentID, Status: "active"}
	q, cancel := f.database.WithContext(f.ctx)
	require.NoError(t, q.Create(&other).Error)
	var platform shopmodel.Platform
	require.NoError(t, q.Select("id").Where("code = ?", "shopee").Take(&platform).Error)
	own := shopmodel.Shop{TenantID: 1, PlatformID: platform.ID, Name: "owned", ExternalShopID: "owned-ext", Timezone: "UTC", Currency: "CNY", OwnerUserID: f.actor.User.ID, DepartmentID: f.actor.User.DepartmentID, Status: "active", Settings: json.RawMessage("{}")}
	foreign := shopmodel.Shop{TenantID: 1, PlatformID: platform.ID, Name: "foreign", ExternalShopID: "foreign-ext", Timezone: "UTC", Currency: "CNY", OwnerUserID: other.ID, DepartmentID: other.DepartmentID, Status: "active", Settings: json.RawMessage("{}")}
	require.NoError(t, q.Create(&own).Error)
	require.NoError(t, q.Create(&foreign).Error)
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id = ? AND code = ?", 1, identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	cancel()
	_, err = service.List(f.ctx, shop.Actor{TenantID: 1, UserID: f.actor.User.ID, DepartmentID: f.actor.User.DepartmentID}, repo.Page{Number: 1, Size: 20, Sort: "-created_at"})
	require.NoError(t, err)

	response, envelope := shopHTTPRequest(t, f.router, http.MethodGet, "/api/v1/admin/shops", nil, f.bearer)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page shop.Page
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 1, page.Total)
	require.Len(t, page.List, 1)
	require.Equal(t, own.ID, page.List[0].ID)

	foreignPath := "/api/v1/admin/shops/" + strconv.FormatInt(foreign.ID, 10)
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		body := any(nil)
		if method == http.MethodPut {
			body = map[string]any{"name": "blocked", "version": foreign.Version}
		}
		response, envelope = shopHTTPRequest(t, f.router, method, foreignPath, body, f.bearer)
		require.Equal(t, http.StatusNotFound, response.Code, method)
		require.Equal(t, 30101, envelope.Code, method)
	}

	ownPath := "/api/v1/admin/shops/" + strconv.FormatInt(own.ID, 10)
	response, envelope = shopHTTPRequest(t, f.router, http.MethodDelete, ownPath, nil, f.bearer)
	require.Equal(t, http.StatusNoContent, response.Code)
	require.Zero(t, envelope.Code)
	response, envelope = shopHTTPRequest(t, f.router, http.MethodGet, ownPath, nil, f.bearer)
	require.Equal(t, http.StatusNotFound, response.Code)
	require.Equal(t, 30101, envelope.Code)
	response, envelope = shopHTTPRequest(t, f.router, http.MethodGet, "/api/v1/admin/shops", nil, f.bearer)
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.Zero(t, page.Total)
}

func shopHTTPRequest(t *testing.T, router http.Handler, method, path string, payload any, bearer string) (*httptest.ResponseRecorder, shopHTTPEnvelope) {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+bearer)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var envelope shopHTTPEnvelope
	if response.Code != http.StatusNoContent {
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	}
	return response, envelope
}

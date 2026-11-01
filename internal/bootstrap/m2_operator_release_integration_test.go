package bootstrap

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
	identitymodel "github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/stretchr/testify/require"
)

func TestM2OperatorPermissionReleaseAndRevokeIntegration(t *testing.T) {
	f := newAdminFixture(t)
	profiles := cachedAdminRoutes(t, f)
	sealer, err := crypto.NewKeyring(map[int][]byte{1: shopRandomBytes(t, 32)}, 1)
	require.NoError(t, err)
	shopService, err := shop.NewService(newShopStore(f.database), f.store.Authorize, sealer)
	require.NoError(t, err)
	require.NoError(t, shop.RegisterRoutes(f.router, f.identityFixture.service, profiles, shopService))

	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	role := operatorRole(t, f)
	own, foreign := createOperatorShops(t, f)
	release := newPermissionReleaseService(t, f)

	applyRelease(t, f, release, []string{permission.ShopStoreList, permission.ShopStoreRead}, "m2-shop")
	operator := f.login(t)

	response, envelope := shopHTTPRequest(t, f.router, http.MethodGet, "/api/v1/admin/shops", nil, operator.AccessToken)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page shop.Page
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, own.ID, page.List[0].ID)

	response, envelope = shopHTTPRequest(t, f.router, http.MethodGet, "/api/v1/admin/shops/"+itoa(foreign.ID), nil, operator.AccessToken)
	require.Equal(t, http.StatusNotFound, response.Code)
	require.Equal(t, 30101, envelope.Code)
	response, envelope = shopHTTPRequest(t, f.router, http.MethodPost, "/api/v1/admin/shops/"+itoa(own.ID)+"/credentials", map[string]any{
		"kind": "api_key", "label": "operator", "values": map[string]string{"value": randomReleaseValue(t)},
	}, operator.AccessToken)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, 20107, envelope.Code)

	result := applyRelease(t, f, release, []string{permission.ShopCredentialList, permission.ShopCredentialCreate}, "m2-credential")
	require.True(t, result.Changed)
	postInvalidation(t, f, result.AffectedUser)

	response, envelope = shopHTTPRequest(t, f.router, http.MethodGet, "/api/v1/admin/shops/"+itoa(own.ID)+"/credentials", nil, operator.AccessToken)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var credentials shop.CredentialPage
	require.NoError(t, json.Unmarshal(envelope.Data, &credentials))
	require.Zero(t, credentials.Total)
	response, envelope = shopHTTPRequest(t, f.router, http.MethodPost, "/api/v1/admin/shops/"+itoa(own.ID)+"/credentials", map[string]any{
		"kind": "api_key", "label": "operator", "values": map[string]string{"value": randomReleaseValue(t)},
	}, operator.AccessToken)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	require.NotZero(t, envelope.Data)

	permissionIDs := permissionIDs(t, f, permission.TradeOrderList, permission.ShopStoreList, permission.ShopStoreRead)
	response, _ = adminRequest(t, f, http.MethodPut, resourcePath("roles", role.ID)+"/permissions", map[string]any{"permission_ids": permissionIDs}, "")
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	response, envelope = shopHTTPRequest(t, f.router, http.MethodGet, "/api/v1/admin/shops/"+itoa(own.ID)+"/credentials", nil, operator.AccessToken)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, 20107, envelope.Code)
}

func newPermissionReleaseService(t *testing.T, f *adminFixture) *identity.PermissionReleaseService {
	t.Helper()
	service, err := identity.NewPermissionReleaseService(NewPermissionReleaseStore(f.database), func(ctx context.Context, event audit.Event) error {
		return audit.Record(ctx, f.database, event)
	})
	require.NoError(t, err)
	return service
}

func applyRelease(t *testing.T, f *adminFixture, service *identity.PermissionReleaseService, codes []string, releaseID string) identity.PermissionReleaseResult {
	t.Helper()
	result, err := service.Apply(f.ctx, 1, identity.PermissionReleaseRequest{RoleCode: identity.OperatorRoleCode, Codes: codes, Release: releaseID})
	require.NoError(t, err)
	return result
}

func postInvalidation(t *testing.T, f *adminFixture, users []int64) {
	t.Helper()
	require.False(t, authz.New(f.redis.cache).Pending(f.ctx, 1, users))
}

func operatorRole(t *testing.T, f *adminFixture) identitymodel.Role {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var role identitymodel.Role
	require.NoError(t, q.Select("id, tenant_id, code, name, data_scope, status").Where("tenant_id = ? AND code = ?", 1, identity.OperatorRoleCode).Take(&role).Error)
	return role
}

func createOperatorShops(t *testing.T, f *adminFixture) (shopmodel.Shop, shopmodel.Shop) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var platform shopmodel.Platform
	require.NoError(t, q.Select("id").Where("code = ?", "shopee").Take(&platform).Error)
	other := identitymodel.User{TenantID: 1, Username: "m2-foreign-owner", PasswordHash: f.user.PasswordHash, DepartmentID: f.user.DepartmentID, Status: "active"}
	require.NoError(t, q.Create(&other).Error)
	own := shopmodel.Shop{TenantID: 1, PlatformID: platform.ID, Name: "m2-owned", ExternalShopID: "m2-owned", Timezone: "UTC", Currency: "CNY", OwnerUserID: f.user.ID, DepartmentID: f.user.DepartmentID, Status: "active", Settings: json.RawMessage("{}")}
	foreign := shopmodel.Shop{TenantID: 1, PlatformID: platform.ID, Name: "m2-foreign", ExternalShopID: "m2-foreign", Timezone: "UTC", Currency: "CNY", OwnerUserID: other.ID, DepartmentID: other.DepartmentID, Status: "active", Settings: json.RawMessage("{}")}
	require.NoError(t, q.Create(&own).Error)
	require.NoError(t, q.Create(&foreign).Error)
	return own, foreign
}

func permissionIDs(t *testing.T, f *adminFixture, codes ...string) []int64 {
	t.Helper()
	rows, err := f.store.Permissions(f.ctx, 1)
	require.NoError(t, err)
	ids := make([]int64, 0, len(codes))
	for _, code := range codes {
		for _, row := range rows {
			if row.Code == code {
				ids = append(ids, row.ID)
				break
			}
		}
	}
	require.Len(t, ids, len(codes))
	return ids
}

func randomReleaseValue(t *testing.T) string {
	t.Helper()
	return hex.EncodeToString(shopRandomBytes(t, 32))
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
	"github.com/stretchr/testify/require"
)

func TestCredentialF2IntegrationTwoRotationsReturnTwoHistoricalVersions(t *testing.T) {
	// F2 §4.7/8: exactly retired metadata is public, never encrypted storage fields.
	f := newCredentialFixture(t)
	credential, _ := f.create(t, "api_key")
	path := credentialPath(credential.ID) + "/versions"
	response, envelope := f.request(t, "GET", path, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.JSONEq(t, `[]`, string(envelope.Data))
	for i := 0; i < 2; i++ {
		response, _ := f.request(t, "PUT", credentialPath(credential.ID), map[string]any{
			"values": map[string]string{"private_value": f.randomValue(t)},
		})
		require.Equal(t, 200, response.Code, response.Body.String())
	}
	response, envelope = f.request(t, "GET", path, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	validateOpenAPIResponse(t, f2ReadContract(t, "shop.yaml"), "/api/v1/admin/credentials/{cid}/versions", "get", 200, response.Body.Bytes())
	var versions []repo.CredentialVersionView
	require.NoError(t, json.Unmarshal(envelope.Data, &versions))
	require.Len(t, versions, 2)
	require.Equal(t, 2, versions[0].VersionNo)
	require.Equal(t, 1, versions[1].VersionNo)
	for _, version := range versions {
		require.NotNil(t, version.RetiredAt)
		require.NotNil(t, version.PurgeAfter)
	}
	for _, field := range []string{"cipher_text", "nonce", "aad", "key_version", "raw_meta", "private_value"} {
		require.NotContains(t, string(envelope.Data), `"`+field+`"`)
	}
	f.noLeaks(t, "application logs", f.logs.text())
	f.noLeaks(t, "SQL logs", f.sqlLogs.text())
}

func TestCredentialF2IntegrationVersionScopeAndMissing(t *testing.T) {
	// F2 §4.4: scope reductions conceal history even for an existing credential.
	f := newCredentialFixture(t)
	credential, _ := f.create(t, "api_key")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	foreignID := f2ForeignCredential(t, f)
	response, _ := f.request(t, "GET", credentialPath(foreignID)+"/versions", nil)
	require.Equal(t, 404, response.Code)
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id=1 AND code=?", identity.SuperAdminRoleCode).Update("data_scope", "custom").Error)
	for _, path := range []string{credentialPath(credential.ID) + "/versions", "/credentials/9999999/versions"} {
		response, _ := f.request(t, "GET", path, nil)
		require.Equal(t, 404, response.Code)
	}
}

func f2ForeignCredential(t *testing.T, f *credentialFixture) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'f2-foreign','F2 foreign') ON CONFLICT (id) DO NOTHING").Error)
	dept := model.Department{TenantID: 2, Name: "F2 foreign", Path: "f2.foreign", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	user := model.User{TenantID: 2, Username: "f2-credential-owner", PasswordHash: f.user.PasswordHash, DepartmentID: dept.ID, Status: "active"}
	require.NoError(t, q.Create(&user).Error)
	role := model.Role{TenantID: 2, Code: "f2-credential-owner", Name: "F2 foreign", DataScope: "all", Status: "active"}
	require.NoError(t, q.Create(&role).Error)
	require.NoError(t, q.Create(&model.UserRole{TenantID: 2, UserID: user.ID, RoleID: role.ID}).Error)
	parent := shopmodel.Shop{TenantID: 2, PlatformID: f.shop.PlatformID, Name: "F2 foreign", ExternalShopID: "f2-foreign", Timezone: "UTC", Currency: "CNY", OwnerUserID: user.ID, DepartmentID: dept.ID}
	require.NoError(t, q.Create(&parent).Error)
	value, err := json.Marshal(f.randomValue(t))
	require.NoError(t, err)
	view, err := f.service.CreateCredential(f.ctx, shop.Actor{TenantID: 2, UserID: user.ID, DepartmentID: dept.ID}, parent.ID,
		shop.CredentialCreateRequest{Kind: "api_key", Values: map[string]json.RawMessage{"private_value": value}})
	require.NoError(t, err)
	return view.ID
}

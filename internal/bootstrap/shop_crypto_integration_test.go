package bootstrap

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/shop/credential"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/stretchr/testify/require"
)

func shopRandomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	t.Cleanup(func() { clear(b) })
	return b
}

func TestShopCryptoIntegrationModelsAndSameKindCipherSwap(t *testing.T) {
	// REQ-SHP-004: actual bytea rows authenticate the verified credential relationship.
	f := newIdentityFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var platform model.Platform
	require.NoError(t, q.Select("id").Where("code = ?", "shopee").Take(&platform).Error)
	shop := model.Shop{TenantID: 1, PlatformID: platform.ID, Name: "crypto-fixture", ExternalShopID: "crypto-fixture", Timezone: "UTC", Currency: "CNY", OwnerUserID: f.user.ID, DepartmentID: f.user.DepartmentID}
	require.NoError(t, q.Create(&shop).Error)
	require.Zero(t, shop.Version)
	creds := []model.Credential{{TenantID: 1, ShopID: shop.ID, Kind: "oauth2"}, {TenantID: 1, ShopID: shop.ID, Kind: "oauth2"}}
	require.NoError(t, q.Create(&creds).Error)
	k, err := crypto.NewKeyring(map[int][]byte{1: shopRandomBytes(t, 32)}, 1)
	require.NoError(t, err)
	versions := make([]model.CredentialVersion, 2)
	for i, c := range creds {
		plain := shopRandomBytes(t, 64)
		aad, err := (crypto.CredentialAAD{TenantID: 1, ShopID: shop.ID, CredentialID: c.ID, Kind: c.Kind, VersionNo: 1}).Encode()
		require.NoError(t, err)
		cipher, nonce, keyVersion, err := k.Seal(plain, aad)
		require.NoError(t, err)
		v := model.CredentialVersion{TenantID: 1, CredentialID: c.ID, VersionNo: 1, CipherText: cipher, Nonce: nonce, KeyVersion: keyVersion, AAD: aad, Fingerprint: crypto.Fingerprint(plain), CreatedReason: "initial"}
		require.NoError(t, q.Create(&v).Error)
		require.NoError(t, q.Select("id, tenant_id, credential_id, version_no, cipher_text, nonce, key_version, aad, fingerprint").Where("id = ?", v.ID).Take(&versions[i]).Error)
		opened, err := credential.OpenVersion(k, 1, shop, c, versions[i])
		require.NoError(t, err)
		require.True(t, bytes.Equal(plain, opened), "stored bytea round trip differs")
		clear(opened)
		require.Len(t, versions[i].Fingerprint, 12)
	}
	for i, c := range creds {
		other := versions[1-i]
		// Include the untrusted aad column in the database swap.
		require.NoError(t, q.Model(&model.CredentialVersion{}).Where("id = ?", versions[i].ID).Updates(map[string]any{"cipher_text": other.CipherText, "nonce": other.Nonce, "key_version": other.KeyVersion, "aad": other.AAD}).Error)
		var swapped model.CredentialVersion
		require.NoError(t, q.Select("id, tenant_id, credential_id, version_no, cipher_text, nonce, key_version, aad").Where("id = ?", versions[i].ID).Take(&swapped).Error)
		opened, err := credential.OpenVersion(k, 1, shop, c, swapped)
		require.Error(t, err)
		require.Nil(t, opened)
	}
}

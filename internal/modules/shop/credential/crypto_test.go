package credential

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/stretchr/testify/require"
)

func randomMaterial(t *testing.T, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	_, err := rand.Read(value)
	require.NoError(t, err)
	t.Cleanup(func() { clear(value) })
	return value
}

func encryptedVersion(t *testing.T, k crypto.Sealer, shop model.Shop, credential model.Credential, plain []byte) model.CredentialVersion {
	t.Helper()
	aad, err := (crypto.CredentialAAD{TenantID: shop.TenantID, ShopID: shop.ID, CredentialID: credential.ID, Kind: credential.Kind, VersionNo: 1}).Encode()
	require.NoError(t, err)
	c, n, v, err := k.Seal(plain, aad)
	require.NoError(t, err)
	return model.CredentialVersion{ID: credential.ID + 100, TenantID: shop.TenantID, CredentialID: credential.ID, VersionNo: 1, AAD: aad, CipherText: c, Nonce: n, KeyVersion: v}
}

func TestSameShopSameKindCredentialsRejectCiphertextSwap(t *testing.T) {
	// REQ-SHP-004: credential_id prevents swaps even when the other four dimensions match.
	k, err := crypto.NewKeyring(map[int][]byte{1: randomMaterial(t, 32)}, 1)
	require.NoError(t, err)
	shop := model.Shop{ID: 2, TenantID: 1}
	credentials := []model.Credential{{ID: 3, TenantID: 1, ShopID: 2, Kind: "oauth2"}, {ID: 4, TenantID: 1, ShopID: 2, Kind: "oauth2"}}
	plains := [][]byte{randomMaterial(t, 64), randomMaterial(t, 64)}
	versions := []model.CredentialVersion{encryptedVersion(t, k, shop, credentials[0], plains[0]), encryptedVersion(t, k, shop, credentials[1], plains[1])}
	for i, credential := range credentials {
		original := versions[i]
		opened, err := OpenVersion(k, 1, shop, credential, original)
		require.NoError(t, err)
		require.True(t, bytes.Equal(plains[i], opened), "positive decryption differs")
		clear(opened)
		swapped := original
		other := versions[1-i]
		swapped.CipherText, swapped.Nonce, swapped.KeyVersion = other.CipherText, other.Nonce, other.KeyVersion
		for _, replaceStoredAAD := range []bool{false, true} {
			if replaceStoredAAD {
				swapped.AAD = other.AAD
			}
			opened, err = OpenVersion(k, 1, shop, credential, swapped)
			require.Error(t, err)
			require.Nil(t, opened)
			require.Equal(t, 30501, errs.From(err).Code)
			require.NotContains(t, err.Error(), string(plains[i]))
		}
		// Prove the authentication tag also rejects the swap independently of the stored-AAD check.
		opened, err = k.Open(other.CipherText, other.Nonce, original.AAD, other.KeyVersion)
		require.Error(t, err)
		require.Nil(t, opened)
	}
}

func TestOpenVersionRebuildsAADOnlyFromValidatedRelationships(t *testing.T) {
	k, err := crypto.NewKeyring(map[int][]byte{1: randomMaterial(t, 32)}, 1)
	require.NoError(t, err)
	shop := model.Shop{ID: 2, TenantID: 1}
	credential := model.Credential{ID: 3, TenantID: 1, ShopID: 2, Kind: "cookie"}
	plain := randomMaterial(t, 64)
	version := encryptedVersion(t, k, shop, credential, plain)
	for _, tc := range []struct {
		name   string
		change func(*model.Shop, *model.Credential, *model.CredentialVersion)
	}{
		{"shop tenant", func(s *model.Shop, _ *model.Credential, _ *model.CredentialVersion) { s.TenantID++ }},
		{"credential tenant", func(_ *model.Shop, c *model.Credential, _ *model.CredentialVersion) { c.TenantID++ }},
		{"version tenant", func(_ *model.Shop, _ *model.Credential, v *model.CredentialVersion) { v.TenantID++ }},
		{"wrong shop", func(_ *model.Shop, c *model.Credential, _ *model.CredentialVersion) { c.ShopID++ }},
		{"wrong credential", func(_ *model.Shop, _ *model.Credential, v *model.CredentialVersion) { v.CredentialID++ }},
		{"wrong kind", func(_ *model.Shop, c *model.Credential, _ *model.CredentialVersion) { c.Kind = "session" }},
		{"wrong version number", func(_ *model.Shop, _ *model.Credential, v *model.CredentialVersion) { v.VersionNo++ }},
		{"forged stored aad", func(_ *model.Shop, _ *model.Credential, v *model.CredentialVersion) { v.AAD = "1:2:4:cookie:1" }},
		{"deleted parent", func(s *model.Shop, _ *model.Credential, _ *model.CredentialVersion) { s.DeletedAt.Valid = true }},
		{"unpersisted version", func(_ *model.Shop, _ *model.Credential, v *model.CredentialVersion) { v.ID = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, v := shop, credential, version
			tc.change(&s, &c, &v)
			opened, err := OpenVersion(k, 1, s, c, v)
			require.Error(t, err)
			require.Nil(t, opened)
		})
	}
	_, err = OpenVersion(k, 9, shop, credential, version)
	require.Error(t, err)
	shop.OwnerUserID, shop.DepartmentID = 9, 10
	opened, err := OpenVersion(k, 1, shop, credential, version)
	require.NoError(t, err)
	clear(opened)
}

type failingSealer struct {
	crypto.Sealer
	partial []byte
	err     error
}

func (s failingSealer) Open([]byte, []byte, string, int) ([]byte, error) {
	return s.partial, s.err
}

func TestOpenVersionHidesProviderErrorAndClearsPartialPlaintext(t *testing.T) {
	partial := randomMaterial(t, 64)
	marker := string(partial)
	providerError := errors.New(marker)
	s := failingSealer{partial: partial, err: providerError}
	shop := model.Shop{ID: 2, TenantID: 1}
	c := model.Credential{ID: 3, TenantID: 1, ShopID: 2, Kind: "oauth2"}
	v := model.CredentialVersion{ID: 4, TenantID: 1, CredentialID: 3, VersionNo: 1, AAD: "1:2:3:oauth2:1"}
	opened, err := OpenVersion(s, 1, shop, c, v)
	require.Nil(t, opened)
	require.Error(t, err)
	require.NotContains(t, err.Error(), marker)
	require.NotErrorIs(t, err, providerError)
	require.Nil(t, errors.Unwrap(err))
	require.Equal(t, make([]byte, 64), partial)
}

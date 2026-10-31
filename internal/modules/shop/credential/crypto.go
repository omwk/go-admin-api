// Package credential authenticates encrypted credential versions against trusted relationships.
package credential

import (
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
)

// OpenVersion accepts an already authorized parent shop and rebuilds AAD from its
// tenant/credential/version relationships. Stored AAD is only a consistency check.
// The caller must consume and clear the returned plaintext promptly.
func OpenVersion(sealer crypto.Sealer, tenant int64, shop model.Shop, credential model.Credential, version model.CredentialVersion) ([]byte, error) {
	if sealer == nil || tenant <= 0 || shop.ID <= 0 || credential.ID <= 0 || version.ID <= 0 ||
		shop.TenantID != tenant || credential.TenantID != tenant || version.TenantID != tenant ||
		credential.ShopID != shop.ID || version.CredentialID != credential.ID || shop.DeletedAt.Valid {
		return nil, crypto.DecryptionFailed()
	}
	aad, err := (crypto.CredentialAAD{TenantID: tenant, ShopID: shop.ID, CredentialID: credential.ID, Kind: credential.Kind, VersionNo: version.VersionNo}).Encode()
	if err != nil || version.AAD != aad {
		return nil, crypto.DecryptionFailed()
	}
	plain, err := sealer.Open(version.CipherText, version.Nonce, aad, version.KeyVersion)
	if err != nil {
		clear(plain)
		return nil, crypto.DecryptionFailed()
	}
	return plain, nil
}

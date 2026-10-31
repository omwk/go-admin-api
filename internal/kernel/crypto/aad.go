package crypto

import (
	"errors"
	"fmt"
)

// CredentialAAD is built from a verified tenant/shop/credential/version chain.
type CredentialAAD struct {
	TenantID, ShopID, CredentialID int64
	Kind                           string
	VersionNo                      int
}

// Encode returns the canonical five-dimensional credential AAD.
func (a CredentialAAD) Encode() (string, error) {
	if a.TenantID <= 0 || a.ShopID <= 0 || a.CredentialID <= 0 || a.VersionNo <= 0 {
		return "", errors.New("credential AAD requires a verified relationship")
	}
	switch a.Kind {
	case "oauth2", "api_key", "cookie", "session":
	default:
		return "", errors.New("credential AAD kind is invalid")
	}
	return fmt.Sprintf("%d:%d:%d:%s:%d", a.TenantID, a.ShopID, a.CredentialID, a.Kind, a.VersionNo), nil
}

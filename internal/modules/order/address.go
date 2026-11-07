package order

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
)

// AddressAAD reconstructs trusted tenant/order/field/revision authentication data.
// Revision is the address revision, independent of the encryption key version.
func AddressAAD(tenantID, orderID int64, field string, revision int64) (string, error) {
	if tenantID <= 0 || orderID <= 0 || revision <= 0 {
		return "", errors.New("invalid address identity")
	}
	switch field {
	case "receiver_name", "phone", "address":
	default:
		return "", errors.New("invalid address field")
	}
	return fmt.Sprintf("%d:%d:%s:%d", tenantID, orderID, field, revision), nil
}

// SealAddress writes independent envelopes and returns no cleartext or unsafe errors.
func SealAddress(sealer crypto.Sealer, tenantID, orderID, revision int64, plain AddressInput) (Address, error) {
	result := Address{TenantID: tenantID, OrderID: orderID, Revision: revision}
	if sealer == nil {
		return Address{}, errors.New("address sealer is required")
	}
	fields := []struct {
		name          string
		plain         []byte
		cipher, nonce *[]byte
	}{
		{"receiver_name", plain.ReceiverName, &result.ReceiverNameCipher, &result.ReceiverNameNonce},
		{"phone", plain.Phone, &result.PhoneCipher, &result.PhoneNonce},
		{"address", plain.Address, &result.AddressCipher, &result.AddressNonce},
	}
	for _, field := range fields {
		aad, err := AddressAAD(tenantID, orderID, field.name, revision)
		if err != nil {
			return Address{}, err
		}
		cipher, nonce, version, err := sealer.Seal(field.plain, aad)
		if err != nil || len(nonce) != 12 || len(cipher) < 16 || version <= 0 {
			return Address{}, errors.New("seal order address failed")
		}
		if result.KeyVersion != 0 && result.KeyVersion != version {
			return Address{}, errors.New("address key version changed")
		}
		result.KeyVersion = version
		*field.cipher, *field.nonce = cipher, nonce
	}
	if bytes.Equal(result.ReceiverNameNonce, result.PhoneNonce) || bytes.Equal(result.PhoneNonce, result.AddressNonce) || bytes.Equal(result.ReceiverNameNonce, result.AddressNonce) {
		return Address{}, errors.New("address nonces must be independent")
	}
	return result, nil
}

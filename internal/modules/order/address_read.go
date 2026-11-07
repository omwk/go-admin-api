package order

import (
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// AddressView is either the bounded stored mask or an explicitly authorized disclosure.
type AddressView struct {
	ReceiverName string `json:"receiver_name"`
	Phone        string `json:"phone"`
	Address      string `json:"address"`
}

// MaskedAddress builds a projection without opening any ciphertext.
func MaskedAddress(name, phone string) AddressView {
	if name == "" {
		name = "*"
	}
	if phone == "" {
		phone = "***********"
	}
	return AddressView{ReceiverName: name, Phone: phone, Address: "**省**市**"}
}

// OpenAddress authenticates all fields before exposing any plaintext.
func OpenAddress(sealer crypto.Sealer, tenant, orderID int64, row Address) (AddressInput, error) {
	if sealer == nil || row.TenantID != tenant || row.OrderID != orderID || row.KeyVersion <= 0 {
		return AddressInput{}, addressDecryptFailed()
	}
	var out AddressInput
	fields := []struct {
		name          string
		cipher, nonce []byte
		target        *[]byte
	}{
		{"receiver_name", row.ReceiverNameCipher, row.ReceiverNameNonce, &out.ReceiverName},
		{"phone", row.PhoneCipher, row.PhoneNonce, &out.Phone},
		{"address", row.AddressCipher, row.AddressNonce, &out.Address},
	}
	for _, field := range fields {
		aad, err := AddressAAD(tenant, orderID, field.name, row.Revision)
		if err != nil {
			clearAddress(out)
			return AddressInput{}, addressDecryptFailed()
		}
		plain, err := sealer.Open(field.cipher, field.nonce, aad, row.KeyVersion)
		if err != nil {
			clear(plain)
			clearAddress(out)
			return AddressInput{}, addressDecryptFailed()
		}
		*field.target = plain
	}
	return out, nil
}

func clearAddress(input AddressInput) {
	clear(input.ReceiverName)
	clear(input.Phone)
	clear(input.Address)
}
func addressDecryptFailed() error { return errs.New(50401, 500, "收货信息解密失败") }

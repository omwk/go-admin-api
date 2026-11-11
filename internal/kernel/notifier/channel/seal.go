package channel

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
)

type envelope struct {
	Cipher  []byte `json:"cipher"`
	Nonce   []byte `json:"nonce"`
	Version int    `json:"key_version"`
}

// ProviderAAD binds ciphertext to trusted tenant, provider and kind values.
func ProviderAAD(p Provider) string {
	tenant := int64(0)
	if p.TenantID != nil {
		tenant = *p.TenantID
	}
	return fmt.Sprintf("notification-provider:v1:%d:%d:%s", tenant, p.ID, p.Kind)
}

// SealJSON encrypts a JSON value without retaining its plaintext buffer.
func SealJSON(s crypto.Sealer, aad string, value any) (json.RawMessage, error) {
	if s == nil {
		return nil, errors.New("notification encryption unavailable")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("notification encoding failed")
	}
	defer clear(raw)
	c, n, v, err := s.Seal(raw, aad)
	if err != nil {
		return nil, errors.New("notification encryption failed")
	}
	result, err := json.Marshal(envelope{c, n, v})
	return result, err
}

// OpenJSON authenticates the envelope against independently reconstructed AAD.
func OpenJSON(s crypto.Sealer, aad string, raw json.RawMessage, value any) error {
	if s == nil {
		return errors.New("notification encryption unavailable")
	}
	var e envelope
	if json.Unmarshal(raw, &e) != nil {
		return errors.New("notification decryption failed")
	}
	plain, err := s.Open(e.Cipher, e.Nonce, aad, e.Version)
	if err != nil {
		return errors.New("notification decryption failed")
	}
	defer clear(plain)
	if json.Unmarshal(plain, value) != nil {
		return errors.New("notification decryption failed")
	}
	return nil
}

func outboxAAD(tenant, id int64) string {
	return fmt.Sprintf("notification-outbox:v1:%d:%d", tenant, id)
}

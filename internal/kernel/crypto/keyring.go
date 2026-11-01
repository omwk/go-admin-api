// Package crypto provides versioned AES-256-GCM with deployment-injected keys.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// Sealer reads an explicit key version and writes only the active version.
type Sealer interface {
	Seal(plaintext []byte, aad string) (cipher, nonce []byte, keyVersion int, err error)
	Open(cipher, nonce []byte, aad string, keyVersion int) ([]byte, error)
}

// Keyring owns immutable key schedules; it never retains caller-owned key buffers.
type Keyring struct {
	keys   map[int]cipher.AEAD
	active int
}

var _ Sealer = (*Keyring)(nil)

// NewKeyring validates all AES-256 keys and freezes a single active write version.
func NewKeyring(keys map[int][]byte, active int) (*Keyring, error) {
	if active <= 0 || len(keys) == 0 {
		return nil, errors.New("crypto keyring and active version are required")
	}
	k := &Keyring{keys: make(map[int]cipher.AEAD, len(keys)), active: active}
	for version, key := range keys {
		if version <= 0 || len(key) != 32 {
			return nil, errors.New("crypto keyring requires positive versions and 32-byte keys")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, errors.New("initialize AES-256")
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, errors.New("initialize GCM")
		}
		k.keys[version] = aead
	}
	if _, exists := k.keys[active]; !exists {
		return nil, errors.New("crypto active version is absent from keyring")
	}
	return k, nil
}

// Seal generates a fresh 12-byte nonce for every encryption.
func (k *Keyring) Seal(plaintext []byte, aad string) ([]byte, []byte, int, error) {
	if k == nil || k.keys[k.active] == nil {
		return nil, nil, 0, errors.New("crypto keyring is not initialized")
	}
	aead := k.keys[k.active]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, 0, errors.New("generate encryption nonce")
	}
	return aead.Seal(nil, nonce, plaintext, []byte(aad)), nonce, k.active, nil
}

// Open selects exactly the stored version; it never tries fallback keys.
func (k *Keyring) Open(ciphertext, nonce []byte, aad string, keyVersion int) ([]byte, error) {
	if k == nil || k.keys[keyVersion] == nil {
		return nil, DecryptionFailed()
	}
	aead := k.keys[keyVersion]
	if len(nonce) != aead.NonceSize() {
		return nil, DecryptionFailed()
	}
	plain, err := aead.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return nil, DecryptionFailed()
	}
	return plain, nil
}

// DecryptionFailed hides all cryptographic inputs and underlying error material.
func DecryptionFailed() error { return errs.New(30501, 500, "凭据解密失败") }

// Fingerprint returns the first twelve hex digits of the plaintext SHA-256 digest.
func Fingerprint(plaintext []byte) string {
	digest := sha256.Sum256(plaintext)
	return hex.EncodeToString(digest[:6])
}

package crypto

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// FromEnvironment loads CH_CRYPTO_KEYS (JSON version -> base64 key) and
// CH_CRYPTO_ACTIVE_VERSION. The deployment owns key storage; no defaults exist.
// Errors name configuration fields only, never their values.
func FromEnvironment(getenv func(string) string) (*Keyring, error) {
	if getenv == nil {
		return nil, errors.New("crypto environment source is required")
	}
	active, err := strconv.Atoi(getenv("CH_CRYPTO_ACTIVE_VERSION"))
	if err != nil {
		return nil, errors.New("crypto.active_version must be a positive integer")
	}
	var encoded map[int]string
	if err := json.Unmarshal([]byte(getenv("CH_CRYPTO_KEYS")), &encoded); err != nil {
		return nil, errors.New("crypto.keys must map versions to base64 keys")
	}
	keys := make(map[int][]byte, len(encoded))
	defer func() {
		for _, key := range keys {
			clear(key)
		}
	}()
	for version, value := range encoded {
		key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "base64:"))
		if err != nil {
			clear(key)
			return nil, errors.New("crypto.keys must contain base64 encoding of 32 bytes")
		}
		keys[version] = key
	}
	return NewKeyring(keys, active)
}

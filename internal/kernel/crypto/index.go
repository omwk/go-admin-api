package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// BlindIndexer produces domain-separated keyed digests without exposing keys.
// Retain the oldest index key when rotating encryption keys; digests name it.
type BlindIndexer interface {
	BlindIndex(string, []byte) (string, error)
	MatchBlindIndex(string, []byte, string) bool
}

func deriveIndexKey(key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("commercehub/blind-index/key/v1"))
	return mac.Sum(nil)
}

// BlindIndex uses the oldest retained version so encryption rotation preserves deduplication.
func (k *Keyring) BlindIndex(domain string, value []byte) (string, error) {
	if k == nil || len(k.indexKeys[k.indexVersion]) == 0 || domain == "" {
		return "", errors.New("blind index key and domain are required")
	}
	return k.index(domain, value, k.indexVersion), nil
}

func (k *Keyring) index(domain string, value []byte, version int) string {
	mac := hmac.New(sha256.New, k.indexKeys[version])
	mac.Write([]byte(domain))
	mac.Write([]byte{0})
	mac.Write(value)
	return fmt.Sprintf("hmac-sha256:%d:%s", version, hex.EncodeToString(mac.Sum(nil)))
}

// MatchBlindIndex verifies exactly the digest's retained version in constant time.
func (k *Keyring) MatchBlindIndex(domain string, value []byte, stored string) bool {
	parts := strings.Split(stored, ":")
	if k == nil || len(parts) != 3 || parts[0] != "hmac-sha256" {
		return false
	}
	version, err := strconv.Atoi(parts[1])
	if err != nil || len(k.indexKeys[version]) == 0 {
		return false
	}
	return hmac.Equal([]byte(k.index(domain, value, version)), []byte(stored))
}

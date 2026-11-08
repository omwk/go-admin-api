package share

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
)

func generateToken(source io.Reader) (string, error) {
	raw := make([]byte, 16)
	if _, err := io.ReadFull(source, raw); err != nil {
		return "", errors.New("generate share secret")
	}
	return "v1" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func validToken(value string) bool {
	if len(value) != 24 || value[:2] != "v1" {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value[2:])
	return err == nil && len(raw) == 16
}

func tokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

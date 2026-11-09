package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

// CanonicalRequest implements the six newline-separated HMAC protocol fields.
func CanonicalRequest(r *http.Request, timestamp, nonce string, body []byte) (string, error) {
	if r == nil || r.URL == nil || r.URL.Opaque != "" || r.URL.EscapedPath() == "" {
		return "", Invalid()
	}
	path := r.URL.EscapedPath()
	if strings.ContainsAny(path, "\r\n\\") || r.URL.RawPath != "" && path != r.URL.RawPath {
		return "", Invalid()
	}
	query, err := canonicalQuery(r.URL.RawQuery)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return strings.Join([]string{r.Method, path, query, timestamp, nonce, hex.EncodeToString(digest[:])}, "\n"), nil
}

func canonicalQuery(raw string) (string, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", Invalid()
	}
	keys := make([]string, 0, len(values))
	for key, items := range values {
		if len(items) != 1 || key == "" || !utf8.ValidString(key) || strings.ContainsAny(key, "&=\r\n") || !utf8.ValidString(items[0]) {
			return "", Invalid()
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+url.QueryEscape(values[key][0]))
	}
	return strings.Join(parts, "&"), nil
}

func matchesSignature(key []byte, canonical string, supplied []byte) bool {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(canonical))
	return hmac.Equal(mac.Sum(nil), supplied)
}

func decodeSignature(value string) ([]byte, bool) {
	if len(value) != 44 {
		return nil, false
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(value)
	return raw, err == nil && len(raw) == sha256.Size
}

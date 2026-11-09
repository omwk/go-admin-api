// Package client demonstrates the M8 server-to-server HMAC protocol without SDK dependencies.
package client

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// NewRequest signs the exact body and escaped path; each call creates a fresh nonce.
// Secret is the issued base64 text, not its decoded bytes or fingerprint.
func NewRequest(ctx context.Context, method, endpoint, appID, secret string, body []byte) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil || appID == "" || secret == "" {
		return nil, errors.New("valid endpoint and application credentials required")
	}
	query, err := canonicalQuery(r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, errors.New("cannot generate request nonce")
	}
	timestamp, nonce := strconv.FormatInt(time.Now().Unix(), 10), hex.EncodeToString(random[:])
	digest := sha256.Sum256(body)
	canonical := strings.Join([]string{r.Method, r.URL.EscapedPath(), query, timestamp, nonce, hex.EncodeToString(digest[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	r.Header.Set("X-App-Id", appID)
	r.Header.Set("X-Timestamp", timestamp)
	r.Header.Set("X-Nonce", nonce)
	r.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return r, nil
}

func canonicalQuery(raw string) (string, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", errors.New("invalid query encoding")
	}
	keys := make([]string, 0, len(values))
	for key, items := range values {
		if len(items) != 1 || key == "" || !utf8.ValidString(key) || strings.ContainsAny(key, "&=\r\n") || !utf8.ValidString(items[0]) {
			return "", errors.New("query must contain unique valid keys and UTF-8 values")
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

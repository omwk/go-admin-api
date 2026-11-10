package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// SigningRequest chooses v1 or the explicitly versioned, request-key-bound protocol.
// V2 appends LF, "v2", LF, X-App-Id, LF, Idempotency-Key to the six v1 fields.
func SigningRequest(r *http.Request, timestamp, nonce string, body []byte) (string, error) {
	canonical, err := CanonicalRequest(r, timestamp, nonce, body)
	if err != nil {
		return "", err
	}
	version, key := r.Header.Get("X-Signature-Version"), r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("X-Signature-Version")) > 1 || len(r.Header.Values("Idempotency-Key")) > 1 {
		return "", Invalid()
	}
	if version == "" || version == "1" {
		if key != "" {
			return "", Invalid()
		}
		return canonical, nil
	}
	if version != "2" || !validRequestKey(key) {
		return "", Invalid()
	}
	return canonical + "\nv2\n" + r.Header.Get("X-App-Id") + "\n" + key, nil
}

func validRequestKey(key string) bool {
	if len(key) < 1 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:-", c)) {
			return false
		}
	}
	return true
}

func (s *CallService) bindRequest(call *Call, r *http.Request) error {
	if call.BillingMode == "per_call" && (r.Header.Get("X-Signature-Version") != "2" || !validRequestKey(r.Header.Get("Idempotency-Key"))) {
		return Invalid()
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		return nil
	}
	body, err := boundedBody(r)
	if err != nil {
		return err
	}
	canonical, err := CanonicalRequest(r, "", "", body)
	if err != nil {
		return err
	}
	call.IdempotencyKey = key
	sum := sha256.Sum256([]byte(call.AppID + "\n" + canonical))
	call.RequestHash = hex.EncodeToString(sum[:])
	return nil
}

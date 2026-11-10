package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
)

func signBillingRequest(r *http.Request, secret string, body []byte) error {
	r.Header.Set("X-Signature-Version", "2")
	canonical, err := gateway.SigningRequest(r, r.Header.Get("X-Timestamp"), r.Header.Get("X-Nonce"), body)
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	r.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return nil
}

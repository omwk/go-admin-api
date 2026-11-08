package share

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

type browserCredential struct {
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	Admitted  bool   `json:"admitted"`
	Nonce     string `json:"nonce"`
}

type browserEnvelope struct {
	Cipher  []byte `json:"c"`
	Nonce   []byte `json:"n"`
	Version int    `json:"v"`
}

func browserAAD(row Link, version string) string {
	password := "public"
	if row.PasswordHash != nil {
		password = tokenHash(*row.PasswordHash)
	}
	return fmt.Sprintf("share:v1:browser:%d:%d:%s:%d:%s:%s", row.TenantID, row.ID, row.TokenHash, row.ContentVersion, version, password)
}

func (s *LinkService) readCredential(value string, row Link, version string) (browserCredential, bool) {
	if s.sealer == nil || len(value) == 0 || len(value) > 2048 {
		return browserCredential{}, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return browserCredential{}, false
	}
	var envelope browserEnvelope
	if json.Unmarshal(raw, &envelope) != nil {
		return browserCredential{}, false
	}
	plain, err := s.sealer.Open(envelope.Cipher, envelope.Nonce, browserAAD(row, version), envelope.Version)
	if err != nil {
		return browserCredential{}, false
	}
	defer clear(plain)
	var credential browserCredential
	if json.Unmarshal(plain, &credential) != nil {
		return browserCredential{}, false
	}
	now := s.now().UTC().Unix()
	valid := credential.IssuedAt <= now && credential.ExpiresAt > now && credential.ExpiresAt > credential.IssuedAt &&
		credential.ExpiresAt-credential.IssuedAt <= 7200 && credential.Nonce != ""
	return credential, valid
}

func (s *LinkService) sealCredential(row Link, version string, credential browserCredential) (string, time.Time, error) {
	if s.sealer == nil {
		return "", time.Time{}, unavailable()
	}
	plain, err := json.Marshal(credential)
	if err != nil {
		return "", time.Time{}, err
	}
	defer clear(plain)
	cipher, nonce, key, err := s.sealer.Seal(plain, browserAAD(row, version))
	if err != nil {
		return "", time.Time{}, unavailable()
	}
	raw, err := json.Marshal(browserEnvelope{Cipher: cipher, Nonce: nonce, Version: key})
	return base64.RawURLEncoding.EncodeToString(raw), time.Unix(credential.ExpiresAt, 0).UTC(), err
}

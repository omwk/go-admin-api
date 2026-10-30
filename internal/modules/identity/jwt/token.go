// Package jwt issues and verifies HS256 access tokens.
package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/idgen"
)

// AccessTTL is the validity period required by M1 for access tokens.
const AccessTTL = 15 * time.Minute

var (
	// ErrInvalidToken indicates invalid credentials, claims or a signature mismatch.
	ErrInvalidToken = errors.New("invalid access token")
	// ErrTokenExpired indicates that an otherwise valid token has reached its expiration.
	ErrTokenExpired = errors.New("access token expired")
)

var header = []byte(`{"alg":"HS256","typ":"JWT"}`)

// Claims are the only identity fields carried by an access token.
type Claims struct {
	Subject   string `json:"sub"`
	TenantID  int64  `json:"tid"`
	SessionID string `json:"sid"`
	TokenID   string `json:"jti"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
}

// Issue creates a 15-minute access token with a fresh JTI.
func Issue(secret []byte, tenantID int64, userID int64, sessionID string, now time.Time) (string, Claims, error) {
	if tenantID <= 0 || userID <= 0 || sessionID == "" {
		return "", Claims{}, errors.New("tenant, user and session are required")
	}
	tokenID, err := idgen.NewULID()
	if err != nil {
		return "", Claims{}, err
	}
	issuedAt := now.UTC().Unix()
	claims := Claims{
		Subject: strconv.FormatInt(userID, 10), TenantID: tenantID, SessionID: sessionID,
		TokenID: tokenID, IssuedAt: issuedAt, ExpiresAt: now.UTC().Add(AccessTTL).Unix(),
	}
	token, err := Sign(secret, claims)
	if err != nil {
		return "", Claims{}, err
	}
	return token, claims, nil
}

// Sign signs validated claims using HMAC-SHA256.
func Sign(secret []byte, claims Claims) (string, error) {
	if len(secret) == 0 || !validClaims(claims) {
		return "", ErrInvalidToken
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", ErrInvalidToken
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	if _, err := mac.Write([]byte(unsigned)); err != nil {
		return "", ErrInvalidToken
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify validates the signature, claims and expiration against now.
func Verify(secret []byte, token string, now time.Time) (Claims, error) {
	if len(secret) == 0 || token == "" {
		return Claims{}, ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalidToken
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || !validHeader(headerBytes) {
		return Claims{}, ErrInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, secret)
	if _, err := mac.Write([]byte(parts[0] + "." + parts[1])); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return Claims{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var claims Claims
	if json.Unmarshal(payload, &claims) != nil || !validClaims(claims) || claims.IssuedAt > now.UTC().Unix() {
		return Claims{}, ErrInvalidToken
	}
	if now.UTC().Unix() >= claims.ExpiresAt {
		return Claims{}, ErrTokenExpired
	}
	return claims, nil
}

func validHeader(data []byte) bool {
	var value struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	return json.Unmarshal(data, &value) == nil && value.Algorithm == "HS256" && value.Type == "JWT"
}

func validClaims(claims Claims) bool {
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	return err == nil && userID > 0 && claims.TenantID > 0 && claims.SessionID != "" &&
		claims.TokenID != "" && claims.IssuedAt > 0 && claims.ExpiresAt > claims.IssuedAt
}

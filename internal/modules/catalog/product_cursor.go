package catalog

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const productCursorVersion = 1

// ProductCursorPosition is the exclusive keyset boundary for deep product pages.
type ProductCursorPosition struct {
	UpdatedAt time.Time `json:"updated_at"`
	ID        int64     `json:"id"`
}

// ProductCursorSigner authenticates tenant- and query-bound cursor tokens.
type ProductCursorSigner struct{ key []byte }

// ProductCursorClaims are the verified query and continuation claims in a cursor.
type ProductCursorClaims struct {
	TenantID int64                 `json:"tenant_id"`
	Filter   string                `json:"filter"`
	Sort     string                `json:"sort"`
	PageSize int                   `json:"page_size"`
	Version  int                   `json:"version"`
	Page     int                   `json:"page"`
	Position ProductCursorPosition `json:"position"`
	Expires  int64                 `json:"expires"`
}

// NewProductCursorSigner derives a purpose-specific HMAC key from application key material.
func NewProductCursorSigner(secret []byte) (*ProductCursorSigner, error) {
	if len(secret) < 32 {
		return nil, errors.New("product cursor key must contain at least 32 bytes")
	}
	key := append([]byte(nil), secret...)
	return &ProductCursorSigner{key: key}, nil
}

// Sign creates an expiring cursor bound to a tenant, filters, page size and sort.
func (s *ProductCursorSigner) Sign(tenant int64, query ProductListQuery, position ProductCursorPosition, page int) (string, error) {
	if s == nil || len(s.key) == 0 || tenant <= 0 || position.ID <= 0 || position.UpdatedAt.IsZero() || page < 101 {
		return "", errors.New("product cursor claims are invalid")
	}
	filter, err := productFilterFingerprint(query.ProductFilter)
	if err != nil {
		return "", err
	}
	claims := ProductCursorClaims{TenantID: tenant, Filter: filter, Sort: query.Sort, PageSize: query.PageSize, Version: productCursorVersion,
		Page: page, Position: position, Expires: time.Now().UTC().Add(24 * time.Hour).Unix()}
	data, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(s.signature(data)), nil
}

// Parse verifies a cursor and rejects changes to its tenant or query contract.
func (s *ProductCursorSigner) Parse(token string, tenant int64, query ProductListQuery) (ProductCursorClaims, error) {
	if s == nil || tenant <= 0 || len(token) > 4096 {
		return ProductCursorClaims{}, errors.New("product cursor is invalid")
	}
	encoded, signature, ok := splitProductCursor(token)
	if !ok {
		return ProductCursorClaims{}, errors.New("product cursor is invalid")
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return ProductCursorClaims{}, errors.New("product cursor is invalid")
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(provided, s.signature(data)) {
		return ProductCursorClaims{}, errors.New("product cursor signature is invalid")
	}
	var claims ProductCursorClaims
	if err := json.Unmarshal(data, &claims); err != nil {
		return ProductCursorClaims{}, errors.New("product cursor is invalid")
	}
	filter, err := productFilterFingerprint(query.ProductFilter)
	if err != nil {
		return ProductCursorClaims{}, err
	}
	if claims.TenantID != tenant || claims.Filter != filter || claims.Sort != query.Sort || claims.PageSize != query.PageSize ||
		claims.Version != productCursorVersion || claims.Page < 101 || claims.Position.ID <= 0 || claims.Position.UpdatedAt.IsZero() ||
		claims.Expires < time.Now().UTC().Unix() {
		return ProductCursorClaims{}, errors.New("product cursor scope does not match")
	}
	return claims, nil
}

func (s *ProductCursorSigner) signature(data []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, err := mac.Write(append([]byte("commercehub:catalog:product-cursor:v1:"), data...))
	if err != nil {
		return nil
	}
	return mac.Sum(nil)
}

func productFilterFingerprint(filter ProductFilter) (string, error) {
	data, err := json.Marshal(filter)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func splitProductCursor(token string) (string, string, bool) {
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			return token[:i], token[i+1:], i > 0 && i < len(token)-1
		}
	}
	return "", "", false
}

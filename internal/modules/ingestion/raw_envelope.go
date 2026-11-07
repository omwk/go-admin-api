package ingestion

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
)

// ProtectedRawRef authenticates a raw's immutable directory and partition identity.
type ProtectedRawRef struct {
	RawIdentity
	ShopID, ID              int64
	ExternalID, PayloadHash string
	FetchedAt               time.Time
}

// RawProtector preserves complete order inputs inside an independently authenticated envelope.
type RawProtector struct {
	sealer  crypto.Sealer
	indexer crypto.BlindIndexer
}

type rawEnvelope struct {
	Format     int    `json:"format"`
	KeyVersion int    `json:"key_version"`
	Nonce      []byte `json:"nonce"`
	Cipher     []byte `json:"cipher"`
}

// NewRawProtector requires encryption and a domain-separated keyed index implementation.
func NewRawProtector(sealer crypto.Sealer) (*RawProtector, error) {
	indexer, ok := sealer.(crypto.BlindIndexer)
	if sealer == nil || !ok {
		return nil, errors.New("protected order raw requires encryption and keyed indexes")
	}
	return &RawProtector{sealer: sealer, indexer: indexer}, nil
}

// RawIndexDomain binds index equality to one trusted tenant/platform/shop relationship.
func RawIndexDomain(identity RawIdentity, shop int64, purpose string) string {
	return fmt.Sprintf("order/raw/v1/%d/%d/%d/%s", identity.TenantID, identity.PlatformID, shop, purpose)
}

// Index produces safe directory values; the unkeyed canonical hash is never persisted for orders.
func (p *RawProtector) Index(identity RawIdentity, shop int64, external, hash string) (string, string, error) {
	if p == nil || identity.TenantID <= 0 || identity.PlatformID <= 0 || shop <= 0 {
		return "", "", errors.New("protected raw identity is required")
	}
	id, err := p.indexer.BlindIndex(RawIndexDomain(identity, shop, "external"), []byte(external))
	if err != nil {
		return "", "", err
	}
	fingerprint, err := p.indexer.BlindIndex(RawIndexDomain(identity, shop, "payload"), []byte(hash))
	return id, fingerprint, err
}

func rawAAD(ref ProtectedRawRef) (string, error) {
	if ref.TenantID <= 0 || ref.PlatformID <= 0 || ref.ShopID <= 0 || ref.ID <= 0 || ref.ExternalID == "" || ref.PayloadHash == "" || ref.FetchedAt.IsZero() {
		return "", errors.New("invalid protected raw identity")
	}
	return fmt.Sprintf("order_raw:1:%d:%d:%d:%d:%s:%s:%s", ref.TenantID, ref.PlatformID, ref.ShopID, ref.ID, ref.FetchedAt.UTC().Format(time.RFC3339Nano), ref.ExternalID, ref.PayloadHash), nil
}

// Seal encrypts the exact payload, original external identity and request metadata together.
func (p *RawProtector) Seal(ref ProtectedRawRef, item RawItem) (json.RawMessage, error) {
	aad, err := rawAAD(ref)
	if err != nil || p == nil {
		return nil, errors.New("seal protected raw failed")
	}
	plain, err := json.Marshal(rawPlain{ExternalID: item.ExternalID, Payload: []byte(item.Payload), RequestMeta: []byte(item.RequestMeta), APIName: item.APIName, HTTPStatus: item.HTTPStatus, FetchedAt: item.FetchedAt})
	if err != nil {
		return nil, errors.New("encode protected raw failed")
	}
	defer clear(plain)
	cipher, nonce, version, err := p.sealer.Seal(plain, aad)
	if err != nil || len(cipher) < 16 || len(nonce) != 12 || version <= 0 {
		return nil, errors.New("seal protected raw failed")
	}
	return json.Marshal(rawEnvelope{Format: 1, KeyVersion: version, Nonce: nonce, Cipher: cipher})
}

// Open rejects any mismatched identity, version or ciphertext without returning partial data.
func (p *RawProtector) Open(ref ProtectedRawRef, payload json.RawMessage) (RawItem, error) {
	aad, err := rawAAD(ref)
	var envelope rawEnvelope
	if p == nil || err != nil || json.Unmarshal(payload, &envelope) != nil || envelope.Format != 1 {
		return RawItem{}, errors.New("open protected order raw failed")
	}
	plain, err := p.sealer.Open(envelope.Cipher, envelope.Nonce, aad, envelope.KeyVersion)
	defer clear(plain)
	if err != nil {
		return RawItem{}, errors.New("open protected order raw failed")
	}
	var decoded rawPlain
	if json.Unmarshal(plain, &decoded) != nil || !json.Valid(decoded.Payload) || !decoded.FetchedAt.Equal(ref.FetchedAt) {
		return RawItem{}, errors.New("open protected order raw failed")
	}
	item := RawItem{ExternalID: decoded.ExternalID, Payload: decoded.Payload, RequestMeta: decoded.RequestMeta, APIName: decoded.APIName, HTTPStatus: decoded.HTTPStatus, FetchedAt: decoded.FetchedAt}
	hash, err := PayloadHash(item.Payload)
	identity := ref.RawIdentity
	if err != nil || !p.indexer.MatchBlindIndex(RawIndexDomain(identity, ref.ShopID, "external"), []byte(item.ExternalID), ref.ExternalID) || !p.indexer.MatchBlindIndex(RawIndexDomain(identity, ref.ShopID, "payload"), []byte(hash), ref.PayloadHash) {
		clear(item.Payload)
		clear(item.RequestMeta)
		return RawItem{}, errors.New("open protected order raw failed")
	}
	return item, nil
}

type rawPlain struct {
	ExternalID           string
	Payload, RequestMeta []byte
	APIName              string
	HTTPStatus           *int
	FetchedAt            time.Time
}

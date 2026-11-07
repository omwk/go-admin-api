package ingestion

import (
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/stretchr/testify/require"
)

func TestRawEnvelopeProtectsCompleteOrder(t *testing.T) {
	// REQ-ORD-007: only synthetic markers enter the transient plaintext.
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	sealer, err := crypto.NewKeyring(map[int][]byte{1: key}, 1)
	clear(key)
	require.NoError(t, err)
	protector, err := NewRawProtector(sealer)
	require.NoError(t, err)
	item := RawItem{ExternalID: "private-instance", Payload: json.RawMessage(`{"address":"PII-CANARY-123"}`), RequestMeta: json.RawMessage(`{"private":"PII-CANARY-123"}`), FetchedAt: time.Now().UTC().Truncate(time.Microsecond)}
	hash, err := PayloadHash(item.Payload)
	require.NoError(t, err)
	identity := RawIdentity{TenantID: 1, PlatformID: 2}
	external, fingerprint, err := protector.Index(identity, 3, item.ExternalID, hash)
	require.NoError(t, err)
	require.NotEqual(t, item.ExternalID, external)
	ref := ProtectedRawRef{RawIdentity: identity, ShopID: 3, ID: 4, ExternalID: external, PayloadHash: fingerprint, FetchedAt: item.FetchedAt}
	envelope, err := protector.Seal(ref, item)
	require.NoError(t, err)
	require.NotContains(t, string(envelope), "PII-CANARY-123")
	opened, err := protector.Open(ref, envelope)
	require.NoError(t, err)
	require.JSONEq(t, string(item.Payload), string(opened.Payload))
	require.JSONEq(t, string(item.RequestMeta), string(opened.RequestMeta))
	require.Equal(t, item.ExternalID, opened.ExternalID)
	for _, change := range []func(*ProtectedRawRef){
		func(r *ProtectedRawRef) { r.TenantID++ }, func(r *ProtectedRawRef) { r.PlatformID++ },
		func(r *ProtectedRawRef) { r.ShopID++ }, func(r *ProtectedRawRef) { r.ID++ },
		func(r *ProtectedRawRef) { r.FetchedAt = r.FetchedAt.Add(time.Second) },
		func(r *ProtectedRawRef) { r.ExternalID += "x" }, func(r *ProtectedRawRef) { r.PayloadHash += "x" },
	} {
		changed := ref
		change(&changed)
		result, err := protector.Open(changed, envelope)
		require.Error(t, err)
		require.Empty(t, result.Payload)
		require.NotContains(t, err.Error(), "PII-CANARY-123")
	}
	assertRawEnvelopeTampering(t, protector, ref, envelope)
}

func assertRawEnvelopeTampering(t *testing.T, p *RawProtector, ref ProtectedRawRef, payload json.RawMessage) {
	t.Helper()
	for _, change := range []func(*rawEnvelope){
		func(e *rawEnvelope) { e.Format++ }, func(e *rawEnvelope) { e.KeyVersion++ },
		func(e *rawEnvelope) { e.Nonce[0] ^= 1 }, func(e *rawEnvelope) { e.Cipher[0] ^= 1 },
	} {
		var envelope rawEnvelope
		require.NoError(t, json.Unmarshal(payload, &envelope))
		change(&envelope)
		corrupt, err := json.Marshal(envelope)
		require.NoError(t, err)
		result, err := p.Open(ref, corrupt)
		require.Error(t, err)
		require.Empty(t, result.Payload)
		require.Empty(t, result.RequestMeta)
		require.NotContains(t, err.Error(), "PII-CANARY-123")
	}
}

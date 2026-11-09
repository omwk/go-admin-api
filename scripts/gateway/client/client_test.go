package client_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/omwk/go-admin-api/scripts/gateway/client"
	"github.com/stretchr/testify/require"
)

func TestExampleSignatureMatchesServerProtocol(t *testing.T) {
	// REQ-GW-003: independent example must agree with the production canonicalizer.
	random := make([]byte, 32)
	_, err := rand.Read(random)
	require.NoError(t, err)
	secret := base64.StdEncoding.EncodeToString(random)
	for _, query := range []string{"", "?z=&a=hello%20world", "?x=%2b%2F%25&name=%E4%B8%AD%E6%96%87", "?a=~&%E4%B8%AD=+"} {
		t.Run(query, func(t *testing.T) {
			body := []byte("exact body\n")
			r, err := client.NewRequest(context.Background(), "GET", "https://example.invalid/open/v1/a%2Fb"+query, ulid.Make().String(), secret, body)
			require.NoError(t, err)
			canonical, err := gateway.CanonicalRequest(r, r.Header.Get("X-Timestamp"), r.Header.Get("X-Nonce"), body)
			require.NoError(t, err)
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte(canonical))
			signature, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Signature"))
			require.NoError(t, err)
			require.True(t, hmac.Equal(mac.Sum(nil), signature), "example signature must verify")
			require.Len(t, r.Header.Get("X-Nonce"), 32)
		})
	}
}

func TestExampleRejectsAmbiguousQuery(t *testing.T) {
	for _, query := range []string{"?a=1&a=2", "?a=1&%61=2", "?a=%xx", "?a=%FF", "?=empty", "?a%26b=value", "?a;b=c"} {
		r, err := client.NewRequest(context.Background(), http.MethodGet, "https://example.invalid/"+query, ulid.Make().String(), "unused", nil)
		require.Error(t, err)
		require.Nil(t, r)
	}
}

package share

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShareTokenEntropyEncodingAndFailure(t *testing.T) {
	// REQ-SHR-003: secrets are generated at runtime, never stored in fixtures.
	seen := map[string]bool{}
	for range 1000 {
		token, err := generateToken(rand.Reader)
		require.NoError(t, err)
		require.Len(t, token, 24)
		require.True(t, validToken(token))
		require.False(t, seen[token])
		seen[token] = true
		digest := sha256.Sum256([]byte(token))
		require.Equal(t, hex.EncodeToString(digest[:]), tokenHash(token))
	}
	_, err := generateToken(io.LimitReader(bytes.NewReader(nil), 0))
	require.Error(t, err)
	for _, invalid := range []string{"", "v1", "../../private", string(bytes.Repeat([]byte{'a'}, 25))} {
		require.False(t, validToken(invalid))
	}
}

package refresh

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateAndHashRefreshToken(t *testing.T) {
	token, err := Generate()
	require.NoError(t, err)
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	require.NoError(t, err)
	require.Len(t, decoded, TokenBytes)

	hash := Hash(token)
	digest, err := hex.DecodeString(hash)
	require.NoError(t, err)
	require.Len(t, digest, 32)
	if hash == token {
		t.Fatal("refresh token storage value must be a digest")
	}
	other, err := Generate()
	require.NoError(t, err)
	if Hash(other) == hash {
		t.Fatal("different refresh tokens must have different hashes")
	}
}

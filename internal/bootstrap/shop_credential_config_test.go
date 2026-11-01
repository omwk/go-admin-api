package bootstrap

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/stretchr/testify/require"
)

func TestCredentialSealerUsesDeploymentKeyringAndActiveVersion(t *testing.T) {
	keys := map[int][]byte{1: shopRandomBytes(t, 32), 2: shopRandomBytes(t, 32)}
	encoded, err := json.Marshal(map[int]string{1: base64.StdEncoding.EncodeToString(keys[1]), 2: base64.StdEncoding.EncodeToString(keys[2])})
	require.NoError(t, err)
	t.Setenv("CH_CRYPTO_KEYS", string(encoded))
	t.Setenv("CH_CRYPTO_ACTIVE_VERSION", "2")
	sealer, err := credentialSealer(nil)
	require.NoError(t, err)
	plain := shopRandomBytes(t, 64)
	old, err := crypto.NewKeyring(keys, 1)
	require.NoError(t, err)
	cipher, nonce, version, err := old.Seal(plain, "1:2:3:oauth2:1")
	require.NoError(t, err)
	opened, err := sealer.Open(cipher, nonce, "1:2:3:oauth2:1", version)
	require.NoError(t, err)
	require.True(t, bytes.Equal(plain, opened))
	clear(opened)
	_, _, version, err = sealer.Seal(plain, "1:2:3:oauth2:2")
	require.NoError(t, err)
	require.Equal(t, 2, version)
	t.Setenv("CH_CRYPTO_ACTIVE_VERSION", "invalid")
	_, err = credentialSealer(nil)
	require.Error(t, err)
}

func TestCredentialSealerFallsBackToValidatedApplicationConfig(t *testing.T) {
	t.Setenv("CH_CRYPTO_KEYS", "")
	t.Setenv("CH_CRYPTO_ACTIVE_VERSION", "")
	key := base64.StdEncoding.EncodeToString(shopRandomBytes(t, 32))
	for _, tc := range []struct {
		name  string
		cfg   config.Crypto
		valid bool
	}{
		{"valid", config.Crypto{MasterKey: key, KeyVersion: 3}, true},
		{"invalid-key", config.Crypto{MasterKey: "invalid", KeyVersion: 3}, false},
		{"invalid-version", config.Crypto{MasterKey: key, KeyVersion: 0}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sealer, err := credentialSealer(&config.Config{Crypto: tc.cfg})
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			_, _, version, err := sealer.Seal(shopRandomBytes(t, 16), "1:2:3:api_key:1")
			require.NoError(t, err)
			require.Equal(t, 3, version)
		})
	}
}

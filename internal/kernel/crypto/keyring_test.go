package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	t.Cleanup(func() { clear(b) })
	return b
}

func TestKeyringRoundTripAndRandomNonce(t *testing.T) {
	// REQ-SHP-004: AES-256-GCM returns only authenticated plaintext.
	k, err := NewKeyring(map[int][]byte{1: randomBytes(t, 32)}, 1)
	require.NoError(t, err)
	plain := randomBytes(t, 64)
	cipher, nonce, version, err := k.Seal(plain, "1:2:3:oauth2:1")
	require.NoError(t, err)
	require.Len(t, nonce, 12)
	require.Len(t, cipher, len(plain)+16)
	require.Equal(t, 1, version)
	opened, err := k.Open(cipher, nonce, "1:2:3:oauth2:1", version)
	require.NoError(t, err)
	defer clear(opened)
	require.True(t, bytes.Equal(plain, opened), "authenticated round trip differs")
	second, nextNonce, _, err := k.Seal(plain, "1:2:3:oauth2:1")
	require.NoError(t, err)
	require.NotEqual(t, nonce, nextNonce)
	require.NotEqual(t, cipher, second)
}

func TestKeyringReadsOldWritesActiveAndRejectsWrongVersions(t *testing.T) {
	oldKey, newKey := randomBytes(t, 32), randomBytes(t, 32)
	old, err := NewKeyring(map[int][]byte{1: oldKey}, 1)
	require.NoError(t, err)
	plain := randomBytes(t, 64)
	cipher, nonce, _, err := old.Seal(plain, "1:2:3:oauth2:1")
	require.NoError(t, err)
	k, err := NewKeyring(map[int][]byte{1: oldKey, 2: newKey}, 2)
	require.NoError(t, err)
	t.Run("old key remains readable", func(t *testing.T) {
		opened, err := k.Open(cipher, nonce, "1:2:3:oauth2:1", 1)
		require.NoError(t, err)
		defer clear(opened)
		require.True(t, bytes.Equal(plain, opened), "old version differs")
	})
	t.Run("new writes use active version", func(t *testing.T) {
		c, n, v, err := k.Seal(plain, "1:2:3:oauth2:1")
		require.NoError(t, err)
		require.Equal(t, 2, v)
		newOnly, err := NewKeyring(map[int][]byte{2: newKey}, 2)
		require.NoError(t, err)
		opened, err := newOnly.Open(c, n, "1:2:3:oauth2:1", v)
		require.NoError(t, err)
		defer clear(opened)
		require.True(t, bytes.Equal(plain, opened), "active key differs")
	})
	t.Run("wrong and unknown versions rejected", func(t *testing.T) {
		for _, version := range []int{0, -1, 2, 3} {
			opened, err := k.Open(cipher, nonce, "1:2:3:oauth2:1", version)
			require.Error(t, err)
			require.Nil(t, opened)
			require.NotContains(t, err.Error(), string(plain))
		}
	})
}

func TestKeyringAuthenticatesEveryAADDimension(t *testing.T) {
	k, err := NewKeyring(map[int][]byte{1: randomBytes(t, 32)}, 1)
	require.NoError(t, err)
	c, n, v, err := k.Seal(randomBytes(t, 64), "1:2:3:oauth2:1")
	require.NoError(t, err)
	for name, aad := range map[string]string{
		"tenant": "9:2:3:oauth2:1", "shop": "1:9:3:oauth2:1", "credential": "1:2:9:oauth2:1",
		"kind": "1:2:3:api_key:1", "version_no": "1:2:3:oauth2:9",
	} {
		t.Run(name, func(t *testing.T) {
			opened, err := k.Open(c, n, aad, v)
			require.Error(t, err)
			require.Nil(t, opened)
		})
	}
}

func TestKeyringRejectsTamperingAndMalformedNonce(t *testing.T) {
	k, err := NewKeyring(map[int][]byte{1: randomBytes(t, 32)}, 1)
	require.NoError(t, err)
	c, n, v, err := k.Seal(randomBytes(t, 64), "1:2:3:oauth2:1")
	require.NoError(t, err)
	for _, tc := range []struct {
		name          string
		cipher, nonce []byte
	}{
		{"cipher altered", append([]byte(nil), c...), append([]byte(nil), n...)},
		{"nonce altered", append([]byte(nil), c...), append([]byte(nil), n...)},
		{"short nonce", c, n[:11]}, {"long nonce", c, append(n, 0)}, {"missing nonce", c, nil}, {"short cipher", c[:15], n},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "cipher altered" {
				tc.cipher[0] ^= 1
			}
			if tc.name == "nonce altered" {
				tc.nonce[0] ^= 1
			}
			opened, err := k.Open(tc.cipher, tc.nonce, "1:2:3:oauth2:1", v)
			require.Error(t, err)
			require.Nil(t, opened)
		})
	}
}

func TestKeyringRejectsInvalidConfigurationAndFreezesKeys(t *testing.T) {
	key := randomBytes(t, 32)
	for _, tc := range []struct {
		keys   map[int][]byte
		active int
	}{
		{nil, 1}, {map[int][]byte{1: key}, 2}, {map[int][]byte{0: key}, 0},
		{map[int][]byte{1: randomBytes(t, 16)}, 1}, {map[int][]byte{1: key, -1: key}, 1},
	} {
		_, err := NewKeyring(tc.keys, tc.active)
		require.Error(t, err)
	}
	keys := map[int][]byte{1: key}
	k, err := NewKeyring(keys, 1)
	require.NoError(t, err)
	c, n, v, err := k.Seal(randomBytes(t, 64), "1:2:3:oauth2:1")
	require.NoError(t, err)
	clear(key)
	delete(keys, 1)
	opened, err := k.Open(c, n, "1:2:3:oauth2:1", v)
	require.NoError(t, err)
	clear(opened)
}

package crypto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlindIndexDomainIsolationAndRotation(t *testing.T) {
	key := randomBytes(t, 32)
	old, err := NewKeyring(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	digest, err := old.BlindIndex("order/raw/v1/1/2/3/external", []byte("synthetic-instance"))
	require.NoError(t, err)
	rotated, err := NewKeyring(map[int][]byte{1: key, 2: randomBytes(t, 32)}, 2)
	require.NoError(t, err)
	again, err := rotated.BlindIndex("order/raw/v1/1/2/3/external", []byte("synthetic-instance"))
	require.NoError(t, err)
	require.Equal(t, digest, again, "encryption rotation must retain dedup identity")
	require.True(t, rotated.MatchBlindIndex("order/raw/v1/1/2/3/external", []byte("synthetic-instance"), digest))
	for _, domain := range []string{"order/raw/v1/2/2/3/external", "order/raw/v1/1/4/3/external", "order/raw/v1/1/2/4/external", "order/raw/v1/1/2/3/payload"} {
		require.False(t, rotated.MatchBlindIndex(domain, []byte("synthetic-instance"), digest))
	}
	for _, bad := range []string{"", digest + "0", "hmac-sha256:999:0", "hmac-sha256:abc:0"} {
		require.False(t, rotated.MatchBlindIndex("order/raw/v1/1/2/3/external", []byte("synthetic-instance"), bad))
	}
	require.False(t, rotated.MatchBlindIndex("order/raw/v1/1/2/3/external", []byte("different"), digest))
}

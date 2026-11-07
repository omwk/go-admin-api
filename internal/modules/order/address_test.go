package order

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/stretchr/testify/require"
)

func orderRandom(t *testing.T, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	_, err := rand.Read(value)
	require.NoError(t, err)
	t.Cleanup(func() { clear(value) })
	return value
}

func TestOrderAddressIndependentEnvelopesAndAuthentication(t *testing.T) {
	// REQ-ORD-007: no fixed PII or key material is stored in fixtures.
	sealer, err := crypto.NewKeyring(map[int][]byte{2: orderRandom(t, 32)}, 2)
	require.NoError(t, err)
	input := AddressInput{orderRandom(t, 32), orderRandom(t, 32), orderRandom(t, 64)}
	sealed, err := SealAddress(sealer, 1, 2, 3, input)
	require.NoError(t, err)
	require.Equal(t, 2, sealed.KeyVersion)
	require.False(t, bytes.Equal(sealed.ReceiverNameNonce, sealed.PhoneNonce))
	require.False(t, bytes.Equal(sealed.ReceiverNameNonce, sealed.AddressNonce))
	require.False(t, bytes.Equal(sealed.PhoneNonce, sealed.AddressNonce))
	for _, field := range []struct {
		name                 string
		plain, cipher, nonce []byte
	}{
		{"receiver_name", input.ReceiverName, sealed.ReceiverNameCipher, sealed.ReceiverNameNonce},
		{"phone", input.Phone, sealed.PhoneCipher, sealed.PhoneNonce},
		{"address", input.Address, sealed.AddressCipher, sealed.AddressNonce},
	} {
		t.Run(field.name, func(t *testing.T) {
			aad, err := AddressAAD(1, 2, field.name, 3)
			require.NoError(t, err)
			plain, err := sealer.Open(field.cipher, field.nonce, aad, 2)
			require.NoError(t, err)
			require.True(t, bytes.Equal(plain, field.plain), "authenticated plaintext differs")
			clear(plain)
			tampered := bytes.Clone(field.cipher)
			tampered[0] ^= 1
			_, err = sealer.Open(tampered, field.nonce, aad, 2)
			require.Error(t, err)
			for _, wrong := range []string{"9:2:" + field.name + ":3", "1:9:" + field.name + ":3", "1:2:" + field.name + ":4", "1:2:other:3"} {
				_, err = sealer.Open(field.cipher, field.nonce, wrong, 2)
				require.Error(t, err)
			}
		})
	}
	encoded, err := json.Marshal(struct {
		Input  AddressInput
		Stored Address
	}{input, sealed})
	require.NoError(t, err)
	require.JSONEq(t, `{"Input":{},"Stored":{}}`, string(encoded))
}

type failingOrderSealer struct {
	crypto.Sealer
	marker []byte
}

func (s failingOrderSealer) Seal([]byte, string) ([]byte, []byte, int, error) {
	return nil, nil, 0, errors.New(string(s.marker))
}

func TestOrderAddressSealingFailureIsSanitized(t *testing.T) {
	marker := orderRandom(t, 32)
	_, err := SealAddress(failingOrderSealer{marker: marker}, 1, 2, 1, AddressInput{})
	require.EqualError(t, err, "seal order address failed")
	for _, tc := range []struct {
		tenant, order, revision int64
		field                   string
	}{
		{0, 2, 1, "phone"}, {1, 0, 1, "phone"}, {1, 2, 0, "phone"}, {1, 2, 1, "other"},
	} {
		_, err := AddressAAD(tc.tenant, tc.order, tc.field, tc.revision)
		require.Error(t, err)
	}
}

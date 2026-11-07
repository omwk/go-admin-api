package order

import (
	"bytes"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/stretchr/testify/require"
)

func TestOrderAddressReadIsAtomicAndMasked(t *testing.T) {
	// REQ-ORD-006/007: default projections never need decryption.
	sealer, err := crypto.NewKeyring(map[int][]byte{1: orderRandom(t, 32)}, 1)
	require.NoError(t, err)
	input := AddressInput{[]byte("张合成"), []byte("1-CANARY"), []byte("PII-CANARY-123")}
	row, err := SealAddress(sealer, 1, 2, 1, input)
	require.NoError(t, err)
	require.Equal(t, "张*", row.ReceiverNameMask)
	require.Equal(t, "1**********", row.PhoneMask)
	opened, err := OpenAddress(sealer, 1, 2, row)
	require.NoError(t, err)
	require.Equal(t, input, opened)
	for _, field := range []string{"name", "phone", "address"} {
		t.Run(field, func(t *testing.T) {
			changed := row
			switch field {
			case "name":
				changed.ReceiverNameCipher = bytes.Clone(row.ReceiverNameCipher)
				changed.ReceiverNameCipher[0] ^= 1
			case "phone":
				changed.PhoneCipher = bytes.Clone(row.PhoneCipher)
				changed.PhoneCipher[0] ^= 1
			case "address":
				changed.AddressCipher = bytes.Clone(row.AddressCipher)
				changed.AddressCipher[0] ^= 1
			}
			result, err := OpenAddress(sealer, 1, 2, changed)
			require.Error(t, err)
			require.Empty(t, result)
			require.NotContains(t, err.Error(), "PII-CANARY-123")
		})
	}
}

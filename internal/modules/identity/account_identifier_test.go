package identity

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountIdentifierNormalization(t *testing.T) {
	material := make([]byte, 8)
	_, err := rand.Read(material)
	require.NoError(t, err)
	local := "1" + strings.Map(func(r rune) rune { return '0' + r%10 }, hex.EncodeToString(material))[:10]
	for _, tc := range []struct{ input, column, want string }{
		{"operator", "username", "operator"}, {" Person@EXAMPLE.INVALID ", "email", "person@example.invalid"},
		{local, "phone", "+86" + local}, {"+86 (" + local[:3] + ") " + local[3:], "phone", "+86" + local},
	} {
		column, value, err := AccountIdentifier(tc.input)
		require.NoError(t, err)
		require.Equal(t, tc.column, column)
		require.Equal(t, tc.want, value)
	}
	for _, invalid := range []string{"+", "123", "name@@example.invalid", ""} {
		_, _, err := AccountIdentifier(invalid)
		require.Error(t, err)
	}
}
